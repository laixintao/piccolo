package sd

import (
	"context"
	"net/netip"
	"net/url"

	"github.com/go-logr/logr"
)

type ServiceDiscover interface {
	Ready(ctx context.Context) (bool, error)
	Resolve(ctx context.Context, key string, allowSelf bool, count int) (<-chan netip.AddrPort, error)
	Advertise(ctx context.Context, keys []string) error
}

type PiccoloServiceDiscover struct {
	piccoloAddress url.URL
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL) (*PiccoloServiceDiscover, error) {
	return &PiccoloServiceDiscover{
		piccoloAddress: *&piccoloAddress,
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
func (p PiccoloServiceDiscover) 	Resolve(ctx context.Context, key string, allowSelf bool, count int) (<-chan netip.AddrPort, error) {
	return nil, nil
}
