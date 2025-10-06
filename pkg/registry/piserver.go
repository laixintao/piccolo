package registry

import (
	"fmt"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/laixintao/piccolo/internal/mux"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
)

type PiServer struct {
	log                  logr.Logger
	ociClient            oci.Client
	resolveRetries       int
	resolveTimeout       time.Duration
	resolveLatestTag     bool
	maxUploadConnections int
	semaphore            chan struct{}
	sd                   sd.ServiceDiscover
}

type PiServerOption func(*PiServer)


func WithMaxUploadConnection(maxConnection int) PiServerOption {
	return func(r *PiServer) {
		r.maxUploadConnections = maxConnection
		r.semaphore = make(chan struct{}, maxConnection)
	}
}

func NewPiServer(ociClient oci.Client, log logr.Logger, sd sd.ServiceDiscover, opts ...PiServerOption) *PiServer {
	r := &PiServer{
		ociClient:            ociClient,
		log: log,
		sd: sd,
		resolveRetries:       3,
		resolveTimeout:       20 * time.Millisecond,
		resolveLatestTag:     true,
		maxUploadConnections: 5,
		semaphore:            make(chan struct{}, 5),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *PiServer) Server(addr string) (*http.Server, error) {
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

func (r *PiServer) handle(rw mux.ResponseWriter, req *http.Request) {
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
		r.log.Error(rw.Error(), "", kvs...)
	}()
	metrics.HttpRequestsInflight.WithLabelValues(path).Add(1)

	if req.URL.Path == "/healthz" && req.Method == http.MethodGet {
		r.readyHandler(rw, req)
		handler = "ready"
		return
	}
	if strings.HasPrefix(req.URL.Path, "/v2") && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		handler = r.registryHandler(rw, req)
		return
	}
	rw.WriteHeader(http.StatusNotFound)
}


func (r *PiServer) registryHandler(rw mux.ResponseWriter, req *http.Request) string {
	// Quickly return 200 for /v2 to indicate that registry supports v2.
	if path.Clean(req.URL.Path) == "/v2" {
		rw.WriteHeader(http.StatusOK)
		return "v2"
	}

	// Parse out path components from request.
	originalRegistry := req.URL.Query().Get("ns")
	ref, err := parsePathComponents(originalRegistry, req.URL.Path)
	if err != nil {
		rw.WriteError(http.StatusNotFound, fmt.Errorf("could not parse path according to OCI distribution spec: %w", err))
		return "registry"
	}

	// Serve registry endpoints.
	switch ref.kind {
	case referenceKindManifest:
		r.handleManifest(rw, req, ref)
		return "manifest"
	case referenceKindBlob:
		// rate limit on maxUploadConnections
		select {
		case r.semaphore <- struct{}{}:
			defer func() {
				<-r.semaphore
				metrics.HttpRequestsBlobHandlerInflight.WithLabelValues().Add(-1)
			}()
			metrics.HttpRequestsBlobHandlerInflight.WithLabelValues().Add(1)
			r.handleBlob(rw, req, ref)
		default:
			r.log.Info("Max connection reached, refuse this request", "maxUploadConnections", r.maxUploadConnections)
			http.Error(rw, "503 Service Unavailable: Too many connections", http.StatusServiceUnavailable)
		}
		return "blob"
	default:
		rw.WriteError(http.StatusNotFound, fmt.Errorf("unknown reference kind %s", ref.kind))
		return "registry"
	}
}

func (r *PiServer) handleManifest(rw mux.ResponseWriter, req *http.Request, ref reference) {
	if ref.dgst == "" {
		var err error
		ref.dgst, err = r.ociClient.Resolve(req.Context(), ref.name)
		if err != nil {
			rw.WriteError(http.StatusNotFound, fmt.Errorf("could not get digest for image tag %s: %w", ref.name, err))
			return
		}
	}
	b, mediaType, err := r.ociClient.GetManifest(req.Context(), ref.dgst)
	if err != nil {
		rw.WriteError(http.StatusNotFound, fmt.Errorf("could not get manifest content for digest %s: %w", ref.dgst.String(), err))
		return
	}
	rw.Header().Set("Content-Type", mediaType)
	rw.Header().Set("Content-Length", strconv.FormatInt(int64(len(b)), 10))
	rw.Header().Set("Docker-Content-Digest", ref.dgst.String())
	if req.Method == http.MethodHead {
		return
	}
	_, err = rw.Write(b)
	if err != nil {
		r.log.Error(err, "error occurred when writing manifest")
		return
	}
}

func (r *PiServer) handleBlob(rw mux.ResponseWriter, req *http.Request, ref reference) {
	size, err := r.ociClient.Size(req.Context(), ref.dgst)
	if err != nil {
		rw.WriteError(http.StatusInternalServerError, fmt.Errorf("could not determine size of blob with digest %s: %w", ref.dgst.String(), err))
		return
	}
	rw.Header().Set("Accept-Ranges", "bytes")
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	rw.Header().Set("Docker-Content-Digest", ref.dgst.String())
	if req.Method == http.MethodHead {
		return
	}

	rc, err := r.ociClient.GetBlob(req.Context(), ref.dgst)
	if err != nil {
		rw.WriteError(http.StatusInternalServerError, fmt.Errorf("could not get reader for blob with digest %s: %w", ref.dgst.String(), err))
		return
	}
	defer rc.Close()

	http.ServeContent(rw, req, "", time.Time{}, rc)
}


func getClientIP(req *http.Request) string {
	forwardedFor := req.Header.Get("X-Forwarded-For")
	if forwardedFor != "" {
		comps := strings.Split(forwardedFor, ",")
		if len(comps) > 1 {
			return comps[0]
		}
		return forwardedFor
	}
	h, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return ""
	}
	return h
}

func (r *PiServer) readyHandler(rw mux.ResponseWriter, req *http.Request) {
	ok, err := r.sd.Ready(req.Context())
	if err != nil {
		rw.WriteError(http.StatusInternalServerError, fmt.Errorf("could not determine router readiness: %w", err))
		return
	}
	if !ok {
		rw.WriteHeader(http.StatusInternalServerError)
		return
	}
}
