package registry

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"

	"github.com/go-logr/logr"
	"github.com/opencontainers/go-digest"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/laixintao/piccolo/internal/mux"
	"github.com/laixintao/piccolo/pkg/metrics"
)

func TestRegistryMirrorsBlobRanges(t *testing.T) {
	t.Parallel()

	blob := []byte("0123456789abcdef0123456789abcdef")
	dgst := digest.FromBytes(blob)
	pi := NewPiServer(&fakeOCIClient{blobs: map[digest.Digest][]byte{dgst: blob}}, "group", logr.Discard(), &fakeSD{})
	piServer, err := pi.Server("127.0.0.1:0")
	require.NoError(t, err)
	peer := httptest.NewServer(piServer.Handler)
	t.Cleanup(peer.Close)
	peerAddr, err := netip.ParseAddrPort(peer.Listener.Addr().String())
	require.NoError(t, err)

	for _, tc := range []struct {
		name         string
		method       string
		rangeHeader  string
		status       int
		body         string
		contentRange string
		length       int
	}{
		{name: "get", method: http.MethodGet, status: http.StatusOK, body: string(blob), length: len(blob)},
		{name: "head", method: http.MethodHead, status: http.StatusOK, length: len(blob)},
		{name: "prefix", method: http.MethodGet, rangeHeader: "bytes=0-15", status: http.StatusPartialContent, body: "0123456789abcdef", contentRange: "bytes 0-15/32", length: 16},
		{name: "suffix", method: http.MethodGet, rangeHeader: "bytes=-4", status: http.StatusPartialContent, body: "cdef", contentRange: "bytes 28-31/32", length: 4},
		{name: "open-ended", method: http.MethodGet, rangeHeader: "bytes=20-", status: http.StatusPartialContent, body: "456789abcdef", contentRange: "bytes 20-31/32", length: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalRegistry := "range-" + tc.name + ".example"
			hitsBefore := mirrorRangeCounter(t, originalRegistry, "hit")
			missesBefore := mirrorRangeCounter(t, originalRegistry, "miss")
			sd := &fakeSD{peers: []netip.AddrPort{peerAddr}}
			registry := NewRegistry(sd, logr.Discard())
			server, err := registry.Server("127.0.0.1:0")
			require.NoError(t, err)

			req := httptest.NewRequest(tc.method, "/v2/test/image/blobs/"+dgst.String()+"?ns="+originalRegistry, nil)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, req)
			resp := recorder.Result()
			t.Cleanup(func() { resp.Body.Close() })
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.status, resp.StatusCode)
			require.Equal(t, tc.body, string(body))
			require.Equal(t, tc.contentRange, resp.Header.Get("Content-Range"))
			require.Equal(t, strconv.Itoa(tc.length), resp.Header.Get("Content-Length"))
			require.Equal(t, dgst.String(), resp.Header.Get("Docker-Content-Digest"))
			require.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
			require.Equal(t, []string{dgst.String()}, sd.resolvedKeys)
			require.Equal(t, hitsBefore+1, mirrorRangeCounter(t, originalRegistry, "hit"))
			require.Equal(t, missesBefore, mirrorRangeCounter(t, originalRegistry, "miss"))
		})
	}
}

func TestRegistryRejectsFailedRangeResponses(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusRequestedRangeNotSatisfiable, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				http.Error(rw, "peer could not serve the blob", status)
			}))
			t.Cleanup(peer.Close)
			peerAddr, err := netip.ParseAddrPort(peer.Listener.Addr().String())
			require.NoError(t, err)
			originalRegistry := fmt.Sprintf("range-error-%d.example", status)
			registry := NewRegistry(&fakeSD{peers: []netip.AddrPort{peerAddr}}, logr.Discard())
			requestURL := "/v2/test/image/blobs/" + digest.FromString("blob").String() + "?ns=" + originalRegistry

			// Failed peer responses must remain retryable, even for range requests.
			var proxyErr error
			handler, err := mux.NewServeMux(func(rw mux.ResponseWriter, req *http.Request) {
				proxyErr = registry.try(peerAddr, rw, req)
			})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodGet, requestURL, nil)
			req.Header.Set("Range", "bytes=0-15")
			handler.ServeHTTP(httptest.NewRecorder(), req)
			require.Error(t, proxyErr)

			hitsBefore := mirrorRangeCounter(t, originalRegistry, "hit")
			missesBefore := mirrorRangeCounter(t, originalRegistry, "miss")
			server, err := registry.Server("127.0.0.1:0")
			require.NoError(t, err)
			req = httptest.NewRequest(http.MethodGet, requestURL, nil)
			req.Header.Set("Range", "bytes=0-15")
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, req)
			require.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest)
			require.Equal(t, hitsBefore, mirrorRangeCounter(t, originalRegistry, "hit"))
			require.Equal(t, missesBefore+1, mirrorRangeCounter(t, originalRegistry, "miss"))
		})
	}
}

func mirrorRangeCounter(t *testing.T, registry, cache string) float64 {
	t.Helper()
	var metric dto.Metric
	err := metrics.MirrorRequestsTotal.WithLabelValues(registry, cache, string(referenceKindBlob)).Write(&metric)
	require.NoError(t, err)
	return metric.GetCounter().GetValue()
}
