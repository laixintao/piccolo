package registry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestRegistryLatestTagPolicy(t *testing.T) {
	t.Parallel()

	for _, upstream := range []string{"registry.example.com", "registry.example.com:5000", "10.0.0.10:5000", "[2001:db8::1]:5000"} {
		t.Run(upstream, func(t *testing.T) {
			t.Parallel()

			for _, tt := range []struct {
				name         string
				reference    string
				allowLatest  bool
				wantFallback bool
			}{
				{name: "disabled latest falls back", reference: "latest", wantFallback: true},
				{name: "enabled latest mirrors", reference: "latest", allowLatest: true},
				{name: "ordinary tag mirrors", reference: "v1"},
				{name: "latest-like tag mirrors", reference: "latest-amd64"},
				{name: "manifest digest mirrors", reference: "sha256:295c7be079025306c4f1d65997fcf7adb411c88f139ad1d34b537164aa060369"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					const manifest = `{"schemaVersion":2}`
					peerRequests := 0
					transport := latestTagTransportFunc(func(req *http.Request) (*http.Response, error) {
						peerRequests++
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(manifest)),
							Request:    req,
						}, nil
					})
					serviceDiscover := &fakeSD{peers: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:5000")}}
					registry := NewRegistry(serviceDiscover, logr.Discard(), WithResolveLatestTag(tt.allowLatest), WithTransport(transport))
					server, err := registry.Server("127.0.0.1:0")
					require.NoError(t, err)

					req := httptest.NewRequest(http.MethodGet, "/v2/piccolo-lab/probe/manifests/"+tt.reference+"?ns="+url.QueryEscape(upstream), nil)
					rw := httptest.NewRecorder()
					server.Handler.ServeHTTP(rw, req)

					if tt.wantFallback {
						require.Equal(t, http.StatusNotFound, rw.Code)
						require.Empty(t, serviceDiscover.resolvedKeys, "latest policy must bypass service discovery")
						require.Zero(t, peerRequests)
						return
					}
					require.Equal(t, http.StatusOK, rw.Code)
					require.Equal(t, manifest, rw.Body.String())
					require.Len(t, serviceDiscover.resolvedKeys, 1)
					require.Equal(t, 1, peerRequests)
				})
			}
		})
	}
}

type latestTagTransportFunc func(*http.Request) (*http.Response, error)

func (f latestTagTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
