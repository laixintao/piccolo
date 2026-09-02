package registry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestPiHealthzIsLocal(t *testing.T) {
	t.Parallel()

	pi := NewPiServer(nil, "default", logr.Discard(), nil)
	server, err := pi.Server("127.0.0.1:0")
	require.NoError(t, err)

	request := httptest.NewRequest(http.MethodGet, "http://pi.test/healthz", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
}
