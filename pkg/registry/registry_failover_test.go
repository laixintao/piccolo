package registry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestRegistryPeerFailover(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		peerStatus []int // Zero closes the connection without an HTTP response.
		wantStatus int
	}{
		{
			name:       "transport failure then healthy peer",
			peerStatus: []int{0, http.StatusOK},
			wantStatus: http.StatusOK,
		},
		{
			name:       "rejected response then healthy peer",
			peerStatus: []int{http.StatusServiceUnavailable, http.StatusOK},
			wantStatus: http.StatusOK,
		},
		{
			name:       "all connections fail",
			peerStatus: []int{0, 0},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "all responses rejected",
			peerStatus: []int{http.StatusServiceUnavailable, http.StatusNotFound},
			wantStatus: http.StatusNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ociClient, _, _, manifest, _ := newManifestFixtures(t)
			pi, err := NewPiServer(ociClient, "group", logr.Discard(), &fakeSD{}).Server("127.0.0.1:0")
			require.NoError(t, err)

			serviceDiscover := &fakeSD{}
			var requests []*atomic.Int32
			for _, status := range tc.peerStatus {
				count := &atomic.Int32{}
				requests = append(requests, count)
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
					if status == 0 {
						// Deterministically fail a real transport request without relying
						// on an unused port remaining available throughout the test.
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Errorf("hijack peer connection: %v", err)
							return
						}
						_ = conn.Close()
						return
					}
					if status != http.StatusOK {
						http.Error(w, "failed peer response must not reach the client", status)
						return
					}
					pi.Handler.ServeHTTP(w, r)
				}))
				t.Cleanup(peer.Close)
				addr, err := netip.ParseAddrPort(peer.Listener.Addr().String())
				require.NoError(t, err)
				serviceDiscover.peers = append(serviceDiscover.peers, addr)
			}

			registry := NewRegistry(serviceDiscover, logr.Discard(), WithLocalArch("amd64"))
			srv, err := registry.Server("127.0.0.1:0")
			require.NoError(t, err)
			server := httptest.NewServer(srv.Handler)
			t.Cleanup(server.Close)
			client := server.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(server.URL + "/v2/org/single-arch/manifests/v1?ns=harbor.example.com")
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantStatus == http.StatusOK {
				require.Equal(t, manifest, body)
				require.Equal(t, ocispec.MediaTypeImageManifest, resp.Header.Get("Content-Type"))
			} else {
				require.Empty(t, body, "exhausted peers should return a clean fallback response")
			}
			for i, count := range requests {
				require.EqualValues(t, 1, count.Load(), "peer %d was not tried exactly once", i)
			}
		})
	}
}
