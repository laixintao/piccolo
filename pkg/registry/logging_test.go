package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/httputils"
	"github.com/laixintao/piccolo/internal/logging"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type logTestTransport func(*http.Request) (*http.Response, error)

func (f logTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func logEntries(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		require.Equal(t, 1, bytes.Count(line, []byte(`"component":`)), "component must be unambiguous")
		var entry map[string]any
		require.NoError(t, json.Unmarshal(line, &entry))
		require.NotEmpty(t, entry["event"])
		require.NotEmpty(t, entry["msg"])
		entries = append(entries, entry)
	}
	return entries
}

func TestPeerTransferLogCorrelation(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	log := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))
	blob := []byte("content downloaded from a peer")
	dgst := digest.FromBytes(blob)
	peer := netip.MustParseAddrPort("10.0.0.2:5127")
	ociClient := &fakeOCIClient{blobs: map[digest.Digest][]byte{dgst: blob}}
	servingPi, err := NewPiServer(ociClient, "group", log, &fakeSD{}).Server("")
	require.NoError(t, err)
	requestID := "847cd673-878a-4fd1-b267-03688ca43c98"
	transport := logTestTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, peer.String(), req.URL.Host)
		require.Equal(t, requestID, req.Header.Get(logging.RequestIDHeader))
		req.RemoteAddr = "10.0.0.1:45678"
		rw := httptest.NewRecorder()
		servingPi.Handler.ServeHTTP(rw, req)
		return rw.Result(), nil
	})
	discovery := &fakeSD{peers: []netip.AddrPort{peer}}
	downloadingPi, err := NewRegistry(discovery, log, WithTransport(transport)).Server("")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/v2/org/image/blobs/"+dgst.String()+"?ns=harbor.example.com", nil)
	req.RemoteAddr = "10.0.0.9:34567"
	req.Header.Set(logging.RequestIDHeader, requestID)
	rw := httptest.NewRecorder()
	downloadingPi.Handler.ServeHTTP(rw, req)
	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, blob, rw.Body.Bytes())

	entries := logEntries(t, &output)
	require.Len(t, entries, 3)
	for _, entry := range entries {
		require.Equal(t, requestID, entry["request_id"])
		require.Equal(t, "INFO", entry["level"])
		require.Equal(t, float64(len(blob)), entry["bytes_sent"])
		require.Equal(t, float64(200), entry["status"])
	}
	require.Equal(t, "upload", entries[0]["component"])
	require.Equal(t, "10.0.0.1:45678", entries[0]["peer"])
	require.Equal(t, "10.0.0.9", entries[0]["client_ip"])
	require.Equal(t, dgst.String(), entries[0]["digest"])
	require.Equal(t, "download", entries[1]["component"])
	require.Equal(t, peer.String(), entries[1]["peer"])
	require.Equal(t, "peer_download_finished", entries[1]["event"])
	require.Equal(t, "download", entries[2]["component"])
	require.Equal(t, "hit", entries[2]["result"])
}

func TestDownloadMissAndFailureLogs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, reference, result, level string
		resolveErr                     error
		status                         int
	}{
		{"latest skipped", "latest", "latest_tag_skipped", "INFO", nil, 404},
		{"key missing", digest.FromString("missing").String(), "miss", "INFO", httputils.ErrNotFound, 404},
		{"discovery failure", digest.FromString("error").String(), "error", "ERROR", errors.New("Piccolo unavailable"), 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			log := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))
			discovery := &fakeSD{resolveErr: tt.resolveErr}
			srv, err := NewRegistry(discovery, log, WithResolveLatestTag(false)).Server("")
			require.NoError(t, err)
			rw := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rw, httptest.NewRequest(http.MethodHead, "/v2/org/image/manifests/"+tt.reference+"?ns=harbor.example.com", nil))
			require.Equal(t, tt.status, rw.Code)
			entries := logEntries(t, &output)
			require.Len(t, entries, 1)
			require.Equal(t, "download", entries[0]["component"])
			require.Equal(t, tt.result, entries[0]["result"])
			require.Equal(t, tt.level, entries[0]["level"])
			require.NotEmpty(t, entries[0]["request_id"])
			if tt.reference == "latest" {
				require.Empty(t, discovery.resolvedKeys)
			}
		})
	}
}

func TestUploadRangeLogCountsTransferredBytes(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	log := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))
	blob := []byte("0123456789")
	dgst := digest.FromBytes(blob)
	srv, err := NewPiServer(&fakeOCIClient{blobs: map[digest.Digest][]byte{dgst: blob}}, "group", log, &fakeSD{}).Server("")
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/v2/org/image/blobs/"+dgst.String(), nil)
	req.Header.Set("Range", "bytes=2-5")
	rw := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rw, req)
	require.Equal(t, http.StatusPartialContent, rw.Code)
	data, err := io.ReadAll(rw.Result().Body)
	require.NoError(t, err)
	require.Equal(t, "2345", string(data))
	entries := logEntries(t, &output)
	require.Len(t, entries, 1)
	require.Equal(t, float64(4), entries[0]["bytes_sent"])
	require.Equal(t, "bytes 2-5/10", entries[0]["content_range"])
	require.Equal(t, "upload", entries[0]["component"])
}

type brokenResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w brokenResponseWriter) Write(b []byte) (int, error) {
	n, _ := w.ResponseRecorder.Write(b[:min(2, len(b))])
	return n, io.ErrClosedPipe
}

func TestUploadWriteFailureIsLogged(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"blobs", "manifests"} {
		t.Run(kind, func(t *testing.T) {
			var output bytes.Buffer
			log := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))
			client, dgst, _, _, _ := newManifestFixtures(t)
			srv, err := NewPiServer(client, "group", log, &fakeSD{}).Server("")
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodGet, "/v2/org/image/"+kind+"/"+dgst.String(), nil)
			srv.Handler.ServeHTTP(brokenResponseWriter{httptest.NewRecorder()}, req)
			entries := logEntries(t, &output)
			require.Len(t, entries, 1)
			require.Equal(t, "upload", entries[0]["component"])
			require.Equal(t, "ERROR", entries[0]["level"])
			require.Equal(t, float64(2), entries[0]["bytes_sent"])
			require.Contains(t, entries[0]["err"], "closed pipe")
		})
	}
}
