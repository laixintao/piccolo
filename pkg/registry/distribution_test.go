package registry

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestParsePathComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		registry        string
		path            string
		expectedName    string
		expectedDgst    digest.Digest
		expectedRefKind referenceKind
	}{
		{
			name:            "valid manifest tag",
			registry:        "example.com",
			path:            "/v2/foo/bar/manifests/hello-world",
			expectedName:    "example.com/foo/bar:hello-world",
			expectedDgst:    "",
			expectedRefKind: referenceKindManifest,
		},
		{
			name:            "valid blob digest",
			registry:        "docker.io",
			path:            "/v2/library/nginx/blobs/sha256:295c7be079025306c4f1d65997fcf7adb411c88f139ad1d34b537164aa060369",
			expectedName:    "",
			expectedDgst:    digest.Digest("sha256:295c7be079025306c4f1d65997fcf7adb411c88f139ad1d34b537164aa060369"),
			expectedRefKind: referenceKindBlob,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ref, err := parsePathComponents(tt.registry, tt.path)
			require.NoError(t, err)
			require.Equal(t, tt.expectedName, ref.name)
			require.Equal(t, tt.expectedDgst, ref.dgst)
			require.Equal(t, tt.expectedRefKind, ref.kind)
		})
	}
}

func TestParsePathComponentsInvalidPath(t *testing.T) {
	t.Parallel()

	tests := []string{
		"/v2/spegel-org/spegel/v0.0.1",
		"/prefix/v2/library/nginx/manifests/latest",
		"/v2/library/nginx/blobs/not-a-digest",
	}
	for _, path := range tests {
		_, err := parsePathComponents("example.com", path)
		require.Error(t, err)
	}
}

func TestParsePathComponentsMissingRegistry(t *testing.T) {
	t.Parallel()

	_, err := parsePathComponents("", "/v2/spegel-org/spegel/manifests/v0.0.1")
	require.EqualError(t, err, "registry parameter needs to be set for tag references")
}

func TestReferenceHasLatestTagWithRegistryPort(t *testing.T) {
	t.Parallel()

	require.True(t, reference{name: "registry.example:5000/library/alpine:latest"}.hasLatestTag())
	require.False(t, reference{name: "registry.example:5000/library/alpine:3.21"}.hasLatestTag())
	require.False(t, reference{dgst: digest.FromString("content")}.hasLatestTag())
}
