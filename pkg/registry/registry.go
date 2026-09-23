package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/internal/buffer"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/internal/logging"
	"github.com/laixintao/piccolo/internal/mux"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
)

const (
	MirroredHeaderKey = "X-Spegel-Mirrored"
	// ArchHeaderKey carries the architecture of the pulling node so that the
	// serving peer can refuse to serve a platform specific manifest built for
	// a different architecture.
	ArchHeaderKey = "X-Pi-Arch"
)

type Registry struct {
	bufferPool       *buffer.BufferPool
	log              logr.Logger
	sd               sd.ServiceDiscover
	transport        http.RoundTripper
	resolveRetries   int
	resolveTimeout   time.Duration
	resolveLatestTag bool
	localArch        string
	semaphore        chan struct{}
}

type Option func(*Registry)

func WithResolveRetries(resolveRetries int) Option {
	return func(r *Registry) {
		r.resolveRetries = resolveRetries
	}
}

func WithResolveLatestTag(resolveLatestTag bool) Option {
	return func(r *Registry) {
		r.resolveLatestTag = resolveLatestTag
	}
}

func WithResolveTimeout(resolveTimeout time.Duration) Option {
	return func(r *Registry) {
		r.resolveTimeout = resolveTimeout
	}
}

func WithTransport(transport http.RoundTripper) Option {
	return func(r *Registry) {
		r.transport = transport
	}
}

// WithLocalArch overrides the architecture of the node, which defaults to
// the architecture the binary is built for.
func WithLocalArch(arch string) Option {
	return func(r *Registry) {
		r.localArch = arch
	}
}

func NewRegistry(sd sd.ServiceDiscover, log logr.Logger, opts ...Option) *Registry {
	r := &Registry{
		sd:               sd,
		log:              log.WithValues("component", logging.Download),
		resolveRetries:   3,
		resolveTimeout:   2 * time.Second,
		resolveLatestTag: true,
		localArch:        runtime.GOARCH,
		bufferPool:       buffer.NewBufferPool(),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConnsPerHost = 100
		transport.DialContext = (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
		r.transport = transport
	}
	return r
}

func (r *Registry) Server(addr string) (*http.Server, error) {
	m, err := mux.NewServeMux(r.handle)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Addr:     addr,
		Handler:  m,
		ErrorLog: slog.NewLogLogger(logr.ToSlogHandler(r.log.WithValues("event", "server_error")), slog.LevelError),
	}
	return srv, nil
}

// metricsPath returns a low-cardinality label value for the "handler" metric
// label. Any path outside the known, bounded set of registered routes is
// bucketed under "unmatched" so that scanning/probing traffic against
// unregistered paths cannot create unbounded Prometheus label cardinality.
func metricsPath(req *http.Request) string {
	switch {
	case req.URL.Path == "/healthz":
		return "/healthz"
	case strings.HasPrefix(req.URL.Path, "/v2"):
		return "/v2/*"
	default:
		return "unmatched"
	}
}

func (r *Registry) handle(rw mux.ResponseWriter, req *http.Request) {
	start := time.Now()
	req = logging.Request(req, r.log.WithValues("client_ip", getClientIP(req), "registry", req.URL.Query().Get("ns"), "path", req.URL.Path, "method", req.Method))
	log := logr.FromContextOrDiscard(req.Context())
	handler := ""
	result := "error"
	path := metricsPath(req)
	defer func() {
		latency := time.Since(start)
		statusCode := strconv.FormatInt(int64(rw.Status()), 10)

		metrics.HttpRequestsInflight.WithLabelValues(path).Add(-1)
		metrics.HttpRequestDurHistogram.WithLabelValues(path, req.Method, statusCode).Observe(latency.Seconds())
		metrics.HttpResponseSizeHistogram.WithLabelValues(path, req.Method, statusCode).Observe(float64(rw.Size()))

		// Ignore logging requests to healthz to reduce log noise
		if req.URL.Path == "/healthz" {
			return
		}
		if rw.Status() >= 500 || (rw.Error() != nil && result == "hit") {
			result = "error"
		}

		kvs := []interface{}{
			"event", "request_finished",
			"status", rw.Status(),
			"latency", latency.String(),
			"handler", handler,
			"result", result,
			"digest", rw.Header().Get("Docker-Content-Digest"),
			"bytes_sent", rw.Size(),
		}
		if rw.Status() < 500 && result != "error" {
			log.Info("containerd request finished", kvs...)
			return
		}
		log.Error(rw.Error(), "containerd request failed", kvs...)
	}()
	metrics.HttpRequestsInflight.WithLabelValues(path).Add(1)

	if strings.HasPrefix(req.URL.Path, "/v2") && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		log.V(4).Info("containerd request received", "event", "request_started")
		handler, result = r.registryHandler(rw, req)
		return
	}
	rw.WriteHeader(http.StatusNotFound)
}

func (r *Registry) registryHandler(rw mux.ResponseWriter, req *http.Request) (string, string) {
	// Quickly return 200 for /v2 to indicate that registry supports v2.
	if path.Clean(req.URL.Path) == "/v2" {
		rw.WriteHeader(http.StatusOK)
		return "v2", "ok"
	}

	// Parse out path components from request.
	originalRegistry := req.URL.Query().Get("ns")
	ref, err := parsePathComponents(originalRegistry, req.URL.Path)
	if err != nil {
		rw.WriteError(http.StatusNotFound, fmt.Errorf("could not parse path according to OCI distribution spec: %w", err))
		return "registry", "invalid_request"
	}

	// Request with mirror header are proxied.
	if req.Header.Get(MirroredHeaderKey) != "true" {
		// Set mirrored header in request to stop infinite loops
		req.Header.Set(MirroredHeaderKey, "true")
		// Tell the serving peer which architecture we expect, so it can
		// refuse to serve a manifest for another architecture.
		req.Header.Set(ArchHeaderKey, r.localArch)
		return "mirror", r.handleMirror(rw, req, ref)
	}

	rw.WriteError(http.StatusNotFound, errors.New("request has already been mirrored"))
	return "registry", "error"
}

func (r *Registry) handleMirror(rw mux.ResponseWriter, req *http.Request, ref reference) string {
	key := ref.dgst.String()
	if key == "" {
		// Digest keys are content addressed and therefore architecture safe.
		// Tag keys are not: a peer may hold the tag for another architecture
		// only, so resolve tags scoped to the local architecture.
		key = oci.ArchTagKey(ref.name, r.localArch)
	}

	log := logr.FromContextOrDiscard(req.Context()).WithValues("key", key)
	req = req.WithContext(logr.NewContext(req.Context(), log))

	defer func() {
		cacheType := "hit"
		if rw.Status() != http.StatusOK && rw.Status() != http.StatusPartialContent {
			cacheType = "miss"
		}
		metrics.MirrorRequestsTotal.WithLabelValues(ref.originalRegistry, cacheType, string(ref.kind)).Inc()
	}()

	if !r.resolveLatestTag && ref.hasLatestTag() {
		rw.WriteHeader(http.StatusNotFound)
		return "latest_tag_skipped"
	}

	// Resolve mirror with the requested key
	resolveCtx, cancel := context.WithTimeout(req.Context(), r.resolveTimeout)
	defer cancel()
	resolveCtx = logr.NewContext(resolveCtx, log)
	peers, err := r.sd.Resolve(resolveCtx, key, r.resolveRetries)

	if err != nil {
		if errors.Is(err, httputils.ErrNotFound) {
			rw.WriteError(http.StatusNotFound, err)
			return "miss"
		}
		rw.WriteError(http.StatusInternalServerError, fmt.Errorf("error occurred when attempting to resolve mirrors: %w", err))
		return "error"
	}

	for attempt, peer := range peers {
		select {
		case <-req.Context().Done():
			// Request has been closed by server or client. No use continuing.
			rw.WriteError(http.StatusNotFound, fmt.Errorf("mirroring for image component %s has been cancelled: %w", key, resolveCtx.Err()))
			return "canceled"
		default:
			start := time.Now()
			log.V(4).Info("trying peer", "event", "peer_attempt", "peer", peer, "attempt", attempt+1)
			err := r.try(peer, rw, req)
			if err != nil {
				log.Error(err, "peer download failed", "event", "peer_failed", "peer", peer, "attempt", attempt+1, "latency", time.Since(start).String())
			} else {
				if rw.Error() != nil || rw.Status() >= 500 {
					return "error"
				}
				log.Info("peer response forwarded to containerd", "event", "peer_download_finished", "peer", peer, "bytes_sent", rw.Size(), "status", rw.Status(), "latency", time.Since(start).String())
				return "hit"
			}
		}
	}
	log.Info("no peer served the request", "event", "mirror_miss", "peer_count", len(peers))
	rw.WriteHeader(http.StatusNotFound)
	return "miss"
}

func (r *Registry) try(peer netip.AddrPort, rw mux.ResponseWriter, req *http.Request) error {

	// Reject unsuccessful responses without writing to containerd, so another
	// peer can be tried before committing response headers or a body.
	// If proxy fails no response is written and it is tried again against a different mirror.
	// If the response writer has been written to it means that the request was properly proxied.
	succeeded := false
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   peer.String(),
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.BufferPool = r.bufferPool
	proxy.Transport = r.transport
	log := logr.FromContextOrDiscard(req.Context()).WithValues("peer", peer)
	proxy.ErrorLog = slog.NewLogLogger(logr.ToSlogHandler(log.WithValues("event", "proxy_error")), slog.LevelError)
	var proxyErr error
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
		proxyErr = err
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		partial := req.Header.Get("Range") != "" && resp.StatusCode == http.StatusPartialContent
		if resp.StatusCode != http.StatusOK && !partial {
			return fmt.Errorf("unexpected mirror response: %s", resp.Status)
		}
		succeeded = true
		return nil
	}
	proxy.ServeHTTP(rw, req)
	if !succeeded {
		if proxyErr == nil {
			proxyErr = errors.New("peer did not return a successful response")
		}
		return proxyErr
	}
	return nil
}
