package sd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"

	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/internal/logging"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"
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

	// Serialize advertisements and full syncs so the cache follows the order in
	// which Piccolo adds or removes this holder's keys. Other API calls do not wait.
	keyUpdates     *semaphore.Weighted
	advertisements *advertisementCache
	now            func() time.Time
}

type Option func(*PiccoloServiceDiscover)

func WithAdvertiseCacheMaxKeys(maxKeys int) Option {
	return func(p *PiccoloServiceDiscover) {
		p.advertisements.maxKeys = maxKeys
	}
}

func NewPiccoloServiceDiscover(piccoloAddress url.URL, log logr.Logger, piAddr string, group string, opts ...Option) (*PiccoloServiceDiscover, error) {
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
	p := &PiccoloServiceDiscover{
		piccoloAddress: piccoloAddress,
		log:            log.WithValues("component", logging.Piccolo, "group", group, "pi_addr", piAddr),
		httpClient:     httpClient,
		piAddr:         piAddr,
		group:          group,
		keyUpdates:     semaphore.NewWeighted(1),
		advertisements: newAdvertisementCache(DefaultAdvertiseCacheMaxKeys),
		now:            time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.advertisements.maxKeys <= 0 {
		return nil, errors.New("advertise cache max keys must be positive")
	}
	return p, nil
}

func (p *PiccoloServiceDiscover) Ready(ctx context.Context) (bool, error) {
	return true, nil
}

func (p *PiccoloServiceDiscover) Advertise(ctx context.Context, keys []string) error {
	if err := p.keyUpdates.Acquire(ctx, 1); err != nil {
		return err
	}
	defer p.keyUpdates.Release(1)

	now := p.now()
	p.advertisements.prune(now)
	pending := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if p.advertisements.contains(key, now) {
			continue
		}
		pending = append(pending, key)
	}

	ctx = logging.Context(ctx, p.log.WithValues("operation", "advertise"))
	log := logr.FromContextOrDiscard(ctx)
	if len(pending) == 0 {
		log.V(4).Info("skipping duplicate image advertisement", "event", "advertise_skipped",
			"input_key_count", len(keys), "skipped_key_count", len(keys), "cache_ttl", advertiseCacheTTL.String(),
			"cache_key_count", len(p.advertisements.keys), "cache_max_keys", p.advertisements.maxKeys)
		return nil
	}
	if len(pending) < len(keys) {
		log.V(4).Info("filtered previously advertised image keys", "event", "advertise_filtered",
			"input_key_count", len(keys), "key_count", len(pending), "skipped_key_count", len(keys)-len(pending))
	}
	if err := p.post(ctx, "advertise", "api/v1/distribution/advertise", model.ImageAdvertiseRequest{
		Holder: p.piAddr, Keys: pending, Group: p.group,
	}, pending, 10*time.Second, 60*time.Second); err != nil {
		return err
	}
	// Only successful writes start the TTL; cache hits never extend it.
	expires := p.now().Add(advertiseCacheTTL)
	evicted := 0
	for _, key := range pending {
		if p.advertisements.record(key, expires) {
			evicted++
		}
	}
	if evicted > 0 {
		log.V(4).Info("advertisement cache reached capacity", "event", "advertise_cache_evicted",
			"evicted_key_count", evicted, "cache_key_count", len(p.advertisements.keys), "cache_max_keys", p.advertisements.maxKeys)
	}
	return nil
}

func (p *PiccoloServiceDiscover) Resolve(ctx context.Context, key string, count int) (peers []netip.AddrPort, err error) {
	u := p.piccoloAddress
	u.Path = path.Join(u.Path, "api", "v1", "distribution", "findkey")
	params := url.Values{}
	params.Add("group", p.group)
	params.Add("key", key)
	params.Add("count", strconv.Itoa(count))
	params.Add("request_host", strings.Split(p.piAddr, ":")[0])
	u.RawQuery = params.Encode()

	ctx = logging.Context(ctx, p.log.WithValues("operation", "findkey", "key", key, "limit", count, "server", u.Host, "path", u.Path, "method", http.MethodGet))
	log := logr.FromContextOrDiscard(ctx)
	start := time.Now()
	status := 0
	defer func() { logAPIResult(log, start, status, err, true, "peer_count", len(peers), "peers", peers) }()
	log.V(4).Info("querying Piccolo for peers", "event", "api_request_started")

	resolveTimer := prometheus.NewTimer(metrics.ResolveDurHistogram.WithLabelValues())
	resp, err := httputils.DoRequestWithRetry(ctx, http.MethodGet, u.String(), nil,
		map[string]string{"Accept": "application/json", logging.RequestIDHeader: logging.RequestID(ctx)},
		time.Second, 5*time.Second, p.httpClient)
	resolveTimer.ObserveDuration()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode

	var response model.FindKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	for _, holder := range response.Holders {
		peer, err := netip.ParseAddrPort(holder)
		if err != nil {
			log.Error(err, "Piccolo returned an invalid peer address", "event", "invalid_peer", "peer", holder)
			continue
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func (p *PiccoloServiceDiscover) Sync(ctx context.Context, keys []string) error {
	if err := p.keyUpdates.Acquire(ctx, 1); err != nil {
		return err
	}
	defer p.keyUpdates.Release(1)

	// A sync is a complete snapshot. Filtering it would remove cached keys from
	// Piccolo, and retaining keys absent from the snapshot would suppress re-adds.
	err := p.post(ctx, "sync", "api/v1/distribution/sync", model.ImageAdvertiseRequest{
		Holder: p.piAddr, Keys: keys, Group: p.group,
	}, keys, 10*time.Second, 90*time.Second)
	if err != nil {
		// The server may have partially applied a failed sync. Let future image
		// events advertise again instead of trusting the previous cache.
		p.advertisements.clear()
		return err
	}
	p.advertisements.replace(keys, p.now())
	return nil
}

func (p *PiccoloServiceDiscover) DoKeepAlive(ctx context.Context) error {
	return p.post(ctx, "keepalive", "api/v1/keepalive", model.KeepAliveRequest{
		HostAddr: p.piAddr, Group: p.group,
	}, nil, time.Second, 10*time.Second, metrics.KeepAliveTotal)
}

func (p *PiccoloServiceDiscover) post(ctx context.Context, operation, endpoint string, payload any, keys []string,
	singleTimeout, totalTimeout time.Duration, counters ...*prometheus.CounterVec) (err error) {
	u := p.piccoloAddress
	u.Path = path.Join(u.Path, endpoint)
	log := p.log.WithValues("operation", operation, "server", u.Host, "path", u.Path, "method", http.MethodPost)
	if operation != "keepalive" {
		log = log.WithValues("key_count", len(keys))
	}
	ctx = logging.Context(ctx, log)
	log = logr.FromContextOrDiscard(ctx)
	start := time.Now()
	status := 0
	defer func() { logAPIResult(log, start, status, err, false) }()
	log.V(4).Info("sending request to Piccolo", "event", "api_request_started")
	if operation != "keepalive" {
		log.V(4).Info("image keys prepared for Piccolo", "event", "keys_prepared", "keys", keys)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := httputils.DoRequestWithRetry(ctx, http.MethodPost, u.String(), body,
		map[string]string{"Content-Type": "application/json", "Accept": "application/json", logging.RequestIDHeader: logging.RequestID(ctx)},
		singleTimeout, totalTimeout, p.httpClient, counters...)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func logAPIResult(log logr.Logger, start time.Time, status int, err error, notFoundIsMiss bool, values ...any) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	if errors.Is(err, httputils.ErrNotFound) {
		status = http.StatusNotFound
		if notFoundIsMiss {
			result = "miss"
		}
	}
	values = append(values, "event", "api_request_finished", "result", result, "latency", time.Since(start).String())
	if status != 0 {
		values = append(values, "status", status)
	}
	if err != nil && result != "miss" {
		log.Error(err, "Piccolo API request failed", values...)
		return
	}
	log.Info("Piccolo API request finished", values...)
}
