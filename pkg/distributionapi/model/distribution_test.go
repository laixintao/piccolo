package model

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/require"
)

func TestImageSyncRequestAllowsExplicitEmptyKeys(t *testing.T) {
	t.Parallel()

	var request ImageSyncRequest
	err := json.Unmarshal([]byte(`{"holder":"10.0.0.1:5001","group":"default","keys":[]}`), &request)
	require.NoError(t, err)
	require.NoError(t, binding.Validator.ValidateStruct(request))
	require.NotNil(t, request.Keys)
	require.Empty(t, *request.Keys)
}

func TestImageSyncRequestRequiresKeysField(t *testing.T) {
	t.Parallel()

	var request ImageSyncRequest
	err := json.Unmarshal([]byte(`{"holder":"10.0.0.1:5001","group":"default"}`), &request)
	require.NoError(t, err)
	require.Error(t, binding.Validator.ValidateStruct(request))
}
