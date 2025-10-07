package sd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"

	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
)

type ServiceDiscover interface {
	Ready(ctx context.Context) (bool, error)
	Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error)
	Advertise(ctx context.Context, keys []string) error
}

type PiccoloServiceDiscover struct {
	piccoloAddress url.URL
	log            logr.Logger
	httpClient     *http.Client
	piAddr         string
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL, log logr.Logger, piAddr string) (*PiccoloServiceDiscover, error) {
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
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	response, err := httputils.DoRequestWithRetry(ctx,
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
		log.Error(err, "Advertise error", "response", response)
		return err
	} 
	log.Info("Advertise done", "response", response)

	return nil
}

func (p PiccoloServiceDiscover) Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error) {
	p.log.Info("Resolve key", "key", key, "count", count)
	addrs := []netip.AddrPort{
		netip.MustParseAddrPort("192.168.0.1:8080"),
	}
	return addrs, nil
}
