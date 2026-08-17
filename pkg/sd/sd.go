package sd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

type ServiceDiscover interface {
	Ready(ctx context.Context) (bool, error)
	Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error)
	Advertise(ctx context.Context, keys []string) error
	Sync(ctx context.Context, keys []string) error
	DoKeepAlive(ctx context.Context) error
}

type PiccoloServiceDiscover struct {
	piccoloAddress url.URL
	log            logr.Logger
	httpClient     *http.Client
	piAddr         string
	group          string
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL, log logr.Logger, piAddr string, group string) (*PiccoloServiceDiscover, error) {
	if piccoloAddress.Scheme != "http" && piccoloAddress.Scheme != "https" {
		return nil, fmt.Errorf("piccolo API URL must use http or https")
	}
	if piccoloAddress.Host == "" {
		return nil, fmt.Errorf("piccolo API URL must include a host")
	}
	addrPort, err := netip.ParseAddrPort(piAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid Pi listen address %q: %w", piAddr, err)
	}
	if !addrPort.Addr().Unmap().Is4() {
		return nil, fmt.Errorf("Pi listen address must use IPv4")
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,

			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	return &PiccoloServiceDiscover{
		piccoloAddress: piccoloAddress,
		log:            log,
		httpClient:     httpClient,
		piAddr:         piAddr,
		group:          group,
	}, nil
}

func (p PiccoloServiceDiscover) Ready(ctx context.Context) (bool, error) {
	u := p.piccoloAddress
	u.Path = path.Join(u.Path, "healthz")
	resp, err := httputils.DoRequestWithRetry(
		ctx,
		http.MethodGet,
		u.String(),
		nil,
		map[string]string{"Accept": "application/json"},
		time.Second,
		time.Second,
		p.httpClient,
	)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (p PiccoloServiceDiscover) Advertise(ctx context.Context, keys []string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Advertise keys", "count", len(keys))
	url := p.piccoloAddress
	url.Path = path.Join(url.Path, "api", "v1", "distribution", "advertise")
	request := model.ImageAdvertiseRequest{
		Holder: p.piAddr,
		Keys:   keys,
		Group:  p.group,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	resp, err := httputils.DoRequestWithRetry(ctx,
		"POST",
		url.String(),
		body,
		map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		},
		10*time.Second,
		60*time.Second,
		p.httpClient,
	)
	if err != nil {
		log.Error(err, "Advertise error")
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read response body")
		return err
	}
	log.Info("Advertise done", "response", string(responseBody))

	return nil
}

func (p PiccoloServiceDiscover) Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error) {
	p.log.Info("Resolve key", "key", key, "count", count)
	log := logr.FromContextOrDiscard(ctx)
	u := p.piccoloAddress
	u.Path = path.Join(u.Path, "api", "v1", "distribution", "findkey")
	params := url.Values{}
	params.Add("group", p.group)
	params.Add("key", key)
	params.Add("count", strconv.Itoa(count))
	requestAddr, _ := netip.ParseAddrPort(p.piAddr)
	params.Add("request_host", requestAddr.Addr().String())
	u.RawQuery = params.Encode()

	resolveTimer := prometheus.NewTimer(metrics.ResolveDurHistogram.WithLabelValues())
	resp, err := httputils.DoRequestWithRetry(ctx,
		"GET",
		u.String(),
		nil,
		map[string]string{
			"Accept": "application/json",
		},
		1*time.Second,
		5*time.Second,
		p.httpClient,
	)
	resolveTimer.ObserveDuration()
	if err != nil {
		log.Error(err, "Resolve error", "requestAddress", u.String())
		return nil, err
	}
	defer resp.Body.Close()

	var findkeyResp model.FindKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&findkeyResp); err != nil {
		return nil, err
	}
	var addrPorts []netip.AddrPort
	for _, h := range findkeyResp.Holders {
		ap, err := netip.ParseAddrPort(h)
		if err != nil {
			log.Error(err, "Can not convert to net.AddrPort", "host", h)
			continue
		}
		addrPorts = append(addrPorts, ap)
	}
	log.Info("Resolve done, find addrPorts", "addrPorts", addrPorts, "requestAddress", u.String())

	return addrPorts, nil
}

func (p PiccoloServiceDiscover) Sync(ctx context.Context, keys []string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Sync keys", "count", len(keys))
	url := p.piccoloAddress
	url.Path = path.Join(url.Path, "api", "v1", "distribution", "sync")
	request := model.ImageSyncRequest{
		Holder: p.piAddr,
		Keys:   &keys,
		Group:  p.group,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	resp, err := httputils.DoRequestWithRetry(ctx,
		"POST",
		url.String(),
		body,
		map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		},
		10*time.Second,
		90*time.Second,
		p.httpClient,
	)
	if err != nil {
		log.Error(err, "Sync error", "key-count", len(keys))
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read response body")
		return err
	}
	log.Info("Sync done", "response", string(responseBody))

	return nil
}

func (p PiccoloServiceDiscover) DoKeepAlive(ctx context.Context) error {
	log := logr.FromContextOrDiscard(ctx)
	url := p.piccoloAddress
	url.Path = path.Join(url.Path, "api", "v1", "keepalive")
	request := model.KeepAliveRequest{
		HostAddr: p.piAddr,
		Group:    p.group,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	resp, err := httputils.DoRequestWithRetry(ctx,
		"POST",
		url.String(),
		body,
		map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		},
		1*time.Second,
		10*time.Second,
		p.httpClient,
		metrics.KeepAliveTotal,
	)
	if err != nil {
		log.Error(err, "SD keepalive error")
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read response body")
		return err
	}
	log.Info("Keepalive Done", "response", string(responseBody))

	return nil
}
