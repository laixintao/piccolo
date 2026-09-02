package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOptionalPlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expected  string
		wantError bool
	}{
		{name: "legacy empty platform", input: "", expected: ""},
		{name: "amd64", input: "linux/amd64", expected: "linux/amd64"},
		{name: "arm64 alias", input: "linux/aarch64/v8", expected: "linux/arm64"},
		{name: "invalid", input: "linux", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			actual, err := normalizeOptionalPlatform(tt.input)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, actual)
		})
	}
}
