package sd

import (
	"context"
	"encoding/json"
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
)

type ServiceDiscover interface {
	Ready(ctx context.Context) (bool, error)
	Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error)
	Advertise(ctx context.Context, keys []string) error
	Sync(ctx context.Context, keys []string) error
}

type PiccoloServiceDiscover struct {
	piccoloAddress url.URL
	log            logr.Logger
	httpClient     *http.Client
	piAddr         string
	group          string
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL, log logr.Logger, piAddr string, group string) (*PiccoloServiceDiscover, error) {
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
		piccoloAddress: *&piccoloAddress,
		log:            log,
		httpClient:     httpClient,
		piAddr:         piAddr,
		group:          group,
	}, nil
}

func (p PiccoloServiceDiscover) Ready(ctx context.Context) (bool, error) {
	return true, nil
}

func (p PiccoloServiceDiscover) Advertise(ctx context.Context, keys []string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Advertise keys...", "keys", keys)
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
		5*time.Second,
		1*time.Second,
		3*time.Second,
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
	u.RawQuery = params.Encode()

	resp, err := httputils.DoRequestWithRetry(ctx,
		"GET",
		u.String(),
		nil,
		map[string]string{
			"Accept": "application/json",
		},
		5*time.Second,
		1*time.Second,
		3*time.Second,
		p.httpClient,
	)
	if err != nil {
		log.Error(err, "Resolve error")
		return nil, err
	}
	defer resp.Body.Close()

	log.Info("Resolve done")

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
	log.Info("Resolve done, find addrPorts", "addrPorts", addrPorts)

	return addrPorts, nil
}

func (p PiccoloServiceDiscover) Sync(ctx context.Context, keys []string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Sync keys...", "keys", keys)
	url := p.piccoloAddress
	url.Path = path.Join(url.Path, "api", "v1", "distribution", "sync")
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
		1*time.Second,
		3*time.Second,
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
	log.Info("Sync done", "response", string(responseBody))

	return nil
}
