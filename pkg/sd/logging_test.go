package sd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/logging"
	"github.com/stretchr/testify/require"
)

type logTestTransport func(*http.Request) (*http.Response, error)

func (f logTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPiccoloOperationLogs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		operation string
		status    int
		body      string
		level     string
		result    string
	}{
		{"findkey", 200, `{"holders":["10.0.0.2:5127"]}`, "INFO", "ok"},
		{"findkey", 404, `{"message":"key not found"}`, "INFO", "miss"},
		{"findkey", 200, `invalid json`, "ERROR", "error"},
		{"advertise", 200, `{}`, "INFO", "ok"},
		{"sync", 200, `{}`, "INFO", "ok"},
		{"keepalive", 200, `{}`, "INFO", "ok"},
		{"advertise", 404, `{"message":"endpoint not found"}`, "ERROR", "error"},
	} {
		t.Run(fmt.Sprintf("%s/%d/%s", tt.operation, tt.status, tt.result), func(t *testing.T) {
			var output bytes.Buffer
			log := logr.FromSlogHandler(slog.NewJSONHandler(&output, nil))
			p, err := NewPiccoloServiceDiscover(url.URL{Scheme: "http", Host: "piccolo.example"}, log, "10.0.0.1:5127", "test-group")
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodGet, "/v2/image/blobs/sha256:123", nil)
			request.Header.Set(logging.RequestIDHeader, "847cd673-878a-4fd1-b267-03688ca43c98")
			// A download invokes discovery. Discovery must retain its own
			// component while preserving the caller's correlation ID.
			request = logging.Request(request, log.WithValues("component", logging.Download))
			p.httpClient = &http.Client{Transport: logTestTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, logging.RequestID(request.Context()), req.Header.Get(logging.RequestIDHeader))
				if tt.operation == "findkey" {
					require.Equal(t, "sha256:123", req.URL.Query().Get("key"))
					require.Equal(t, "test-group", req.URL.Query().Get("group"))
				} else {
					var payload map[string]any
					require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
					if tt.operation == "keepalive" {
						require.Equal(t, "test-group", payload["Group"])
					} else {
						require.Equal(t, "test-group", payload["group"])
					}
				}
				return &http.Response{
					StatusCode: tt.status, Status: fmt.Sprintf("%d %s", tt.status, http.StatusText(tt.status)),
					Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header), Request: req,
				}, nil
			})}
			ctx := request.Context()
			switch tt.operation {
			case "findkey":
				_, err = p.Resolve(ctx, "sha256:123", 3)
			case "advertise":
				err = p.Advertise(ctx, []string{"sha256:123"})
			case "sync":
				err = p.Sync(ctx, []string{"sha256:123"})
			case "keepalive":
				err = p.DoKeepAlive(ctx)
			}
			if tt.result == "ok" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
			require.Len(t, lines, 1, "one operation summary at INFO")
			require.Equal(t, 1, bytes.Count(lines[0], []byte(`"component":`)))
			var entry map[string]any
			require.NoError(t, json.Unmarshal(lines[0], &entry))
			require.Equal(t, "piccolo", entry["component"])
			require.Equal(t, tt.operation, entry["operation"])
			require.Equal(t, tt.level, entry["level"])
			require.Equal(t, tt.result, entry["result"])
			require.Equal(t, "api_request_finished", entry["event"])
			require.Equal(t, float64(tt.status), entry["status"])
			require.Equal(t, logging.RequestID(ctx), entry["request_id"])
			require.NotEmpty(t, entry["latency"])
			if tt.operation == "advertise" || tt.operation == "sync" {
				require.Equal(t, float64(1), entry["key_count"])
				require.NotContains(t, entry, "keys")
			}
		})
	}
}

func TestPiccoloRetryLogsKeepComponentAndRequestID(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	log := logr.FromSlogHandler(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p, err := NewPiccoloServiceDiscover(url.URL{Scheme: "http", Host: "piccolo.example"}, log, "10.0.0.1:5127", "test")
	require.NoError(t, err)
	var ids []string
	p.httpClient = &http.Client{Transport: logTestTransport(func(req *http.Request) (*http.Response, error) {
		ids = append(ids, req.Header.Get(logging.RequestIDHeader))
		status := http.StatusOK
		if len(ids) == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	require.NoError(t, p.DoKeepAlive(context.Background()))
	require.Len(t, ids, 2)
	require.NotEmpty(t, ids[0])
	require.Equal(t, ids[0], ids[1])
	var retryFound bool
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(line, &entry))
		require.Equal(t, "piccolo", entry["component"])
		require.Equal(t, "keepalive", entry["operation"])
		require.Equal(t, ids[0], entry["request_id"])
		if entry["event"] == "api_attempt_failed" {
			retryFound = true
			require.Equal(t, float64(1), entry["attempt"])
			require.Equal(t, float64(503), entry["status"])
		}
	}
	require.True(t, retryFound)
}
