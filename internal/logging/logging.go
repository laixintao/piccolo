// Package logging defines the three Pi work flows and their request correlation.
package logging

import (
	"context"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
)

const (
	Upload          = "upload"
	Download        = "download"
	Piccolo         = "piccolo"
	RequestIDHeader = "X-Pi-Request-ID"
)

type requestIDKey struct{}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Context preserves correlation while replacing the logger. In particular,
// Piccolo API calls must use their own component, not the download caller's.
func Context(ctx context.Context, log logr.Logger) context.Context {
	id := RequestID(ctx)
	if id == "" {
		id = uuid.NewString()
		ctx = context.WithValue(ctx, requestIDKey{}, id)
	}
	return logr.NewContext(ctx, log.WithValues("request_id", id))
}

// Request carries the same ID through the downloading Pi and the serving peer.
func Request(req *http.Request, log logr.Logger) *http.Request {
	ctx := req.Context()
	if id, err := uuid.Parse(req.Header.Get(RequestIDHeader)); err == nil {
		ctx = context.WithValue(ctx, requestIDKey{}, id.String())
	}
	ctx = Context(ctx, log)
	req = req.WithContext(ctx)
	req.Header.Set(RequestIDHeader, RequestID(ctx))
	return req
}
