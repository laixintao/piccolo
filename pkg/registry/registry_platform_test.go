package registry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestRegistryUsesPlatformOnlyForTagLookups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		path             string
		incomingPlatform string
		expectedKey      string
		expectedPlatform string
	}{
		{
			name:             "tag",
			path:             "/v2/kube-galio/nginx-debug/manifests/v0.0.1?ns=harbor.shopeemobile.com",
			expectedKey:      "harbor.shopeemobile.com/kube-galio/nginx-debug:v0.0.1",
			expectedPlatform: "linux/arm64",
		},
		{
			name:             "digest",
			path:             "/v2/kube-galio/nginx-debug/manifests/" + testARM64Digest.String() + "?ns=harbor.shopeemobile.com",
			incomingPlatform: "linux/amd64",
			expectedKey:      testARM64Digest.String(),
			expectedPlatform: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discovery := &recordingDiscovery{
				peer: netip.MustParseAddrPort("127.0.0.1:5000"),
			}
			var forwardedPlatform string
			transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				forwardedPlatform = req.Header.Get(PlatformHeaderKey)
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("manifest")),
					Request:    req,
				}, nil
			})
			registry := NewRegistry(discovery, logr.Discard(),
				WithPlatform(ocispec.Platform{OS: "linux", Architecture: "arm64"}),
				WithTransport(transport),
			)
			httpServer, err := registry.Server("")
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodGet, "http://pi"+tt.path, nil)
			if tt.incomingPlatform != "" {
				req.Header.Set(PlatformHeaderKey, tt.incomingPlatform)
			}
			rw := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(rw, req)

			require.Equal(t, http.StatusOK, rw.Code)
			require.Equal(t, tt.expectedKey, discovery.key)
			require.Equal(t, tt.expectedPlatform, discovery.platform)
			require.Equal(t, tt.expectedPlatform, forwardedPlatform)
		})
	}
}

type recordingDiscovery struct {
	peer     netip.AddrPort
	key      string
	platform string
}

func (*recordingDiscovery) Ready(context.Context) (bool, error) { return true, nil }

func (d *recordingDiscovery) Resolve(_ context.Context, key, platform string, _ int) ([]netip.AddrPort, error) {
	d.key = key
	d.platform = platform
	return []netip.AddrPort{d.peer}, nil
}

func (*recordingDiscovery) Advertise(context.Context, []string) error { return nil }

func (*recordingDiscovery) Sync(context.Context, []string) error { return nil }

func (*recordingDiscovery) DoKeepAlive(context.Context) error { return nil }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
