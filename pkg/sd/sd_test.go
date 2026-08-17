package sd

import (
	"net/url"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestNewPiccoloServiceDiscoverValidation(t *testing.T) {
	t.Parallel()

	api, err := url.Parse("https://piccolo.example:7789/base")
	require.NoError(t, err)
	client, err := NewPiccoloServiceDiscover(*api, logr.Discard(), "10.0.0.12:5001", "default")
	require.NoError(t, err)
	require.Equal(t, *api, client.piccoloAddress)

	invalidScheme, _ := url.Parse("ftp://piccolo.example")
	_, err = NewPiccoloServiceDiscover(*invalidScheme, logr.Discard(), "10.0.0.12:5001", "default")
	require.EqualError(t, err, "piccolo API URL must use http or https")

	_, err = NewPiccoloServiceDiscover(*api, logr.Discard(), "not-an-address", "default")
	require.ErrorContains(t, err, "invalid Pi listen address")

	_, err = NewPiccoloServiceDiscover(*api, logr.Discard(), "[2001:db8::1]:5001", "default")
	require.EqualError(t, err, "Pi listen address must use IPv4")
}
