package oci

import (
	"context"
	"net/url"
	"testing"

	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/require"
)

func TestNewContainerd(t *testing.T) {
	t.Parallel()

	registries := mustURLs(t, "https://docker.io", "https://ghcr.io")
	c, err := NewContainerd(context.Background(), "socket", "namespace", registries)
	require.NoError(t, err)
	require.Empty(t, c.contentPath)
	require.Nil(t, c.client)
	require.Equal(t, `name~="^(docker\\.io|ghcr\\.io)/"`, c.listFilter)

	c, err = NewContainerd(
		context.Background(),
		"socket",
		"namespace",
		registries,
		WithContentPath("local"),
	)
	require.NoError(t, err)
	require.Equal(t, "local", c.contentPath)
}

func TestCreateFilters(t *testing.T) {
	t.Parallel()

	listFilter, eventFilter := createFilters(mustURLs(t, "https://docker.io", "https://gcr.io"))
	require.Equal(t, `name~="^(docker\\.io|gcr\\.io)/"`, listFilter)
	require.Equal(t, `topic~="/images/create|/images/update|/images/delete",event.name~="^(docker\\.io|gcr\\.io)/"`, eventFilter)
}

func TestGetEventImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		data              any
		expectedErr       string
		expectedName      string
		expectedEventType EventType
	}{
		{name: "nil event", expectedErr: "any cannot be nil"},
		{name: "unknown event", data: &eventtypes.ContainerCreate{}, expectedErr: "unsupported event type"},
		{name: "create event", data: &eventtypes.ImageCreate{Name: "create"}, expectedName: "create", expectedEventType: CreateEvent},
		{name: "update event", data: &eventtypes.ImageUpdate{Name: "update"}, expectedName: "update", expectedEventType: UpdateEvent},
		{name: "delete event", data: &eventtypes.ImageDelete{Name: "delete"}, expectedName: "delete", expectedEventType: DeleteEvent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var event typeurl.Any
			if tt.data != nil {
				var err error
				event, err = typeurl.MarshalAny(tt.data)
				require.NoError(t, err)
			}

			name, eventType, err := getEventImage(event)
			if tt.expectedErr != "" {
				require.EqualError(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedName, name)
			require.Equal(t, tt.expectedEventType, eventType)
		})
	}
}

func TestValidateRegistries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		registry    string
		expectedErr string
	}{
		{name: "valid HTTP", registry: "http://registry.example:5000"},
		{name: "valid HTTPS", registry: "https://registry.example"},
		{name: "invalid scheme", registry: "ftp://registry.example", expectedErr: "invalid registry url scheme must be http or https: ftp://registry.example"},
		{name: "path", registry: "https://registry.example/v2", expectedErr: "invalid registry url path has to be empty: https://registry.example/v2"},
		{name: "query", registry: "https://registry.example?x=1", expectedErr: "invalid registry url query has to be empty: https://registry.example?x=1"},
		{name: "credentials", registry: "https://user@registry.example", expectedErr: "invalid registry url user has to be empty: https://user@registry.example"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateRegistries(mustURLs(t, tt.registry))
			if tt.expectedErr != "" {
				require.EqualError(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func mustURLs(t *testing.T, values ...string) []url.URL {
	t.Helper()

	urls := make([]url.URL, 0, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		require.NoError(t, err)
		urls = append(urls, *parsed)
	}
	return urls
}
