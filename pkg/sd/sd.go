package sd

import (
	"context"
	"net/netip"
	"net/url"

	"github.com/go-logr/logr"
)

type ServiceDiscover interface {
	Ready(ctx context.Context) (bool, error)
	Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error)
	Advertise(ctx context.Context, keys []string) error
}

type PiccoloServiceDiscover struct {
	piccoloAddress url.URL
	log            logr.Logger
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL, log logr.Logger) (*PiccoloServiceDiscover, error) {
	return &PiccoloServiceDiscover{
		piccoloAddress: *&piccoloAddress,
		log:            log,
	}, nil
}

func (p PiccoloServiceDiscover) Ready(ctx context.Context) (bool, error) {
	return true, nil
}

func (p PiccoloServiceDiscover) Advertise(ctx context.Context, keys []string) error {
	log := logr.FromContextOrDiscard(ctx)
	log.Info("Advertise keys...", "keys", keys)
	return nil
}

// TODO
func (p PiccoloServiceDiscover) Resolve(ctx context.Context, key string, count int) ([]netip.AddrPort, error) {
	p.log.Info("Resolve key", "key", key, "count", count)
	addrs := []netip.AddrPort{
		netip.MustParseAddrPort("192.168.0.1:8080"),
		netip.MustParseAddrPort("192.168.0.2:8080"),
	}
	return addrs, nil
}
