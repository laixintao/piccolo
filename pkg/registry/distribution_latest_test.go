package registry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReferenceHasLatestTag(t *testing.T) {
	t.Parallel()

	for _, registry := range []string{"registry.example.com", "registry.example.com:5000", "10.0.0.10:5000", "[2001:db8::1]:5000"} {
		t.Run(registry, func(t *testing.T) {
			t.Parallel()

			for _, tt := range []struct {
				name string
				path string
				want bool
			}{
				{name: "latest", path: "/v2/piccolo-lab/probe/manifests/latest", want: true},
				{name: "ordinary tag", path: "/v2/piccolo-lab/probe/manifests/v1"},
				{name: "tag with latest prefix", path: "/v2/piccolo-lab/probe/manifests/latest-amd64"},
				{name: "tag with latest suffix", path: "/v2/piccolo-lab/probe/manifests/v1-latest"},
				{name: "case sensitive tag", path: "/v2/piccolo-lab/probe/manifests/Latest"},
				{name: "repository named latest", path: "/v2/piccolo-lab/latest/manifests/v1"},
				{name: "manifest digest", path: "/v2/piccolo-lab/probe/manifests/sha256:295c7be079025306c4f1d65997fcf7adb411c88f139ad1d34b537164aa060369"},
				{name: "blob digest", path: "/v2/piccolo-lab/probe/blobs/sha256:295c7be079025306c4f1d65997fcf7adb411c88f139ad1d34b537164aa060369"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					ref, err := parsePathComponents(registry, tt.path)
					require.NoError(t, err)
					require.Equal(t, tt.want, ref.hasLatestTag())
				})
			}
		})
	}

	require.False(t, (reference{}).hasLatestTag())
}
