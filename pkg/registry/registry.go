package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/internal/buffer"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/internal/mux"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/sd"
)

const (
	MirroredHeaderKey = "X-Spegel-Mirrored"
)

type Registry struct {
	bufferPool       *buffer.BufferPool
	log              logr.Logger
	sd               sd.ServiceDiscover
	transport        http.RoundTripper
	resolveRetries   int
	resolveTimeout   time.Duration
	resolveLatestTag bool
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

func NewRegistry(sd sd.ServiceDiscover, log logr.Logger, opts ...Option) *Registry {
	r := &Registry{
		sd:               sd,
		log:              log,
		resolveRetries:   3,
		resolveTimeout:   2 * time.Second,
		resolveLatestTag: true,
		bufferPool:       buffer.NewBufferPool(),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.transport == nil {
		//nolint: errcheck // Ignore
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConnsPerHost = 100
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
		Addr:    addr,
		Handler: m,
	}
	return srv, nil
}

func (r *Registry) handle(rw mux.ResponseWriter, req *http.Request) {
	start := time.Now()
	handler := ""
	path := req.URL.Path
	if strings.HasPrefix(path, "/v2") {
		path = "/v2/*"
	}
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

		kvs := []interface{}{
			"path", req.URL.Path,
			"status", rw.Status(),
			"method", req.Method,
			"latency", latency.String(),
			"ip", getClientIP(req),
			"handler", handler,
		}
		if rw.Status() >= 200 && rw.Status() < 300 {
			r.log.Info("", kvs...)
			return
		}
		r.log.Error(rw.Error(), "request-to-registry", kvs...)
	}()
	metrics.HttpRequestsInflight.WithLabelValues(path).Add(1)

	if strings.HasPrefix(req.URL.Path, "/v2") && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		handler = r.registryHandler(rw, req)
		return
	}
	rw.WriteHeader(http.StatusNotFound)
}

func (r *Registry) registryHandler(rw mux.ResponseWriter, req *http.Request) string {
	// Quickly return 200 for /v2 to indicate that registry supports v2.
	if path.Clean(req.URL.Path) == "/v2" {
		rw.WriteHeader(http.StatusOK)
		return "v2"
	}

	// Parse out path components from request.
	originalRegistry := req.URL.Query().Get("ns")
	r.log.Info("request v2 registry", "path", req.URL, "method", req.Method)
	ref, err := parsePathComponents(originalRegistry, req.URL.Path)
	if err != nil {
		rw.WriteError(http.StatusNotFound, fmt.Errorf("could not parse path according to OCI distribution spec: %w", err))
		return "registry"
	}

	// Request with mirror header are proxied.
	if req.Header.Get(MirroredHeaderKey) != "true" {
		// Set mirrored header in request to stop infinite loops
		req.Header.Set(MirroredHeaderKey, "true")
		r.handleMirror(rw, req, ref)
		return "mirror"
	}

	r.log.Error(errors.New("request mirrored already"), "This request has already been mirrored")
	return "error"
}

func (r *Registry) handleMirror(rw mux.ResponseWriter, req *http.Request, ref reference) {
	key := ref.dgst.String()
	if key == "" {
		key = ref.name
	}

	log := r.log.WithValues("key", key, "path", req.URL.Path, "ip", getClientIP(req))

	defer func() {
		cacheType := "hit"
		if rw.Status() != http.StatusOK {
			cacheType = "miss"
		}
		metrics.MirrorRequestsTotal.WithLabelValues(ref.originalRegistry, cacheType, string(ref.kind)).Inc()
	}()

	if !r.resolveLatestTag && ref.hasLatestTag() {
		r.log.V(4).Info("skipping mirror request for image with latest tag", "image", ref.name)
		rw.WriteHeader(http.StatusNotFound)
		return
	}

	// Resolve mirror with the requested key
	resolveCtx, cancel := context.WithTimeout(req.Context(), r.resolveTimeout)
	defer cancel()
	resolveCtx = logr.NewContext(resolveCtx, log)
	peers, err := r.sd.Resolve(resolveCtx, key, r.resolveRetries)

	if err != nil {
		if errors.Is(err, httputils.ErrNotFound) {
			rw.WriteError(http.StatusNotFound, err)
			return
		}
		rw.WriteError(http.StatusInternalServerError, fmt.Errorf("error occurred when attempting to resolve mirrors: %w", err))
		return
	}

	for _, peer := range peers {
		select {
		case <-req.Context().Done():
			// Request has been closed by server or client. No use continuing.
			rw.WriteError(http.StatusNotFound, fmt.Errorf("mirroring for image component %s has been cancelled: %w", key, resolveCtx.Err()))
			return
		default:
			err := r.try(peer, rw, req)
			if err != nil {
				r.log.Error(err, "request failed when try peer", "peer", peer)
			} else {
				r.log.Info("Mirror successfully handled")
				return
			}
		}
	}
	r.log.Info("WARN: all peers failed or timeout reached")
	rw.WriteHeader(http.StatusNotFound)
}

func (r *Registry) try(peer netip.AddrPort, rw mux.ResponseWriter, req *http.Request) error {

	// Modify response returns and error on non 200 status code and NOP error handler skips response writing.
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
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
		r.log.Error(err, "request to mirror failed")
		http.Error(rw, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("expected mirror to respond with 200 OK but received: %s", resp.Status)
		}
		succeeded = true
		return nil
	}
	proxy.ServeHTTP(rw, req)
	if !succeeded {
		return errors.New("Fail to mirror request")
	}
	return nil
}
