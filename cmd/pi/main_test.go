package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidateArgs(t *testing.T) {
	t.Parallel()

	valid := Arguments{
		FullRefreshMinutes:          60,
		MaxUploadConnections:        5,
		MaxUploadBlobBytesPerSecond: 1024,
		MirrorResolveTimeout:        time.Second,
		MirrorResolveRetries:        3,
	}
	require.NoError(t, validateArgs(&valid))

	tests := []struct {
		name string
		edit func(*Arguments)
	}{
		{name: "refresh interval", edit: func(a *Arguments) { a.FullRefreshMinutes = 0 }},
		{name: "connections", edit: func(a *Arguments) { a.MaxUploadConnections = 0 }},
		{name: "upload speed", edit: func(a *Arguments) { a.MaxUploadBlobBytesPerSecond = 0 }},
		{name: "resolve timeout", edit: func(a *Arguments) { a.MirrorResolveTimeout = 0 }},
		{name: "resolve retries", edit: func(a *Arguments) { a.MirrorResolveRetries = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := valid
			tt.edit(&args)
			require.Error(t, validateArgs(&args))
		})
	}
}
