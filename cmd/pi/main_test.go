package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
	"github.com/stretchr/testify/require"
)

func TestNamespaceListArguments(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cli     []string
		useEnv  bool
		env     string
		want    []string
		invalid bool
	}{
		{name: "default", want: []string{"k8s.io"}},
		{name: "single CLI value", cli: []string{"--containerd-namespace=default"}, want: []string{"default"}},
		{name: "CLI list", cli: []string{"--containerd-namespace", "k8s.io", "default"}, want: []string{"k8s.io", "default"}},
		{name: "environment list", useEnv: true, env: "k8s.io,default", want: []string{"k8s.io", "default"}},
		{name: "CLI replaces environment list", useEnv: true, env: "k8s.io,default", cli: []string{"--containerd-namespace", "build"}, want: []string{"build"}},
		{name: "empty CLI list", cli: []string{"--containerd-namespace"}, want: []string{}, invalid: true},
		{name: "empty environment list", useEnv: true, env: "", want: []string{}, invalid: true},
		{name: "comma is literal on CLI", cli: []string{"--containerd-namespace=k8s.io,default"}, want: []string{"k8s.io,default"}, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := newArguments()
			argv := []string{
				"--registry-listen-addr", "127.0.0.1:5123",
				"--pi-listen-addr", "127.0.0.1:5127",
				"--metrics-listen-addr", "127.0.0.1:9090",
				"--group", "test",
			}
			if tt.useEnv {
				t.Setenv("CONTAINERD_NAMESPACE", tt.env)
				t.Setenv("REGISTRIES", "https://one.example.com,https://two.example.com")
			} else {
				argv = append(argv, "--registries", "https://one.example.com", "https://two.example.com")
			}
			argv = append(argv, tt.cli...)
			argv = append(argv, "--log-level", "INFO")
			parser, err := arg.NewParser(arg.Config{IgnoreEnv: !tt.useEnv}, args)
			require.NoError(t, err)
			require.NoError(t, parser.Parse(argv))
			require.Equal(t, tt.want, args.ContainerdNamespaces)
			require.Len(t, args.Registries, 2)
			require.Equal(t, "https://one.example.com", args.Registries[0].String())
			require.Equal(t, "https://two.example.com", args.Registries[1].String())
			_, err = oci.NewContainerd(context.Background(), "unused", args.ContainerdNamespaces, args.Registries)
			if tt.invalid {
				require.ErrorContains(t, err, "invalid containerd namespace")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func parseTestArguments(t *testing.T, extra ...string) Arguments {
	t.Helper()
	args := newArguments()
	parser, err := arg.NewParser(arg.Config{IgnoreEnv: true}, args)
	require.NoError(t, err)
	command := []string{
		"--registry-listen-addr", "127.0.0.1:5001",
		"--pi-listen-addr", "127.0.0.1:5002",
		"--metrics-listen-addr", "127.0.0.1:9090",
		"--registries", "http://registry.example:5000",
		"--group", "test",
	}
	require.NoError(t, parser.Parse(append(command, extra...)))
	return *args
}

func TestDefaultRefreshIntervalCanStartTicker(t *testing.T) {
	args := parseTestArguments(t)
	require.NoError(t, args.validate())
	require.NotPanics(t, func() {
		ticker := time.NewTicker(time.Duration(args.FullRefreshMinutes) * time.Minute)
		ticker.Stop()
	}, "omitting an optional CLI argument must not crash the tracker")
}

func TestAdvertiseCacheLimitArguments(t *testing.T) {
	args := parseTestArguments(t)
	require.Equal(t, sd.DefaultAdvertiseCacheMaxKeys, args.AdvertiseCacheMaxKeys)
	for _, limit := range []int{1, 50000, 0, -1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			args := parseTestArguments(t, "--advertise-cache-max-keys", strconv.Itoa(limit))
			require.Equal(t, limit, args.AdvertiseCacheMaxKeys)
			if limit > 0 {
				require.NoError(t, args.validate())
			} else {
				require.ErrorContains(t, args.validate(), "--advertise-cache-max-keys must be positive")
			}
		})
	}
}

func TestAdvertiseCacheLimitEnvironment(t *testing.T) {
	t.Setenv("ADVERTISE_CACHE_MAX_KEYS", "12345")
	args := newArguments()
	parser, err := arg.NewParser(arg.Config{}, args)
	require.NoError(t, err)
	argv := []string{
		"--registry-listen-addr", "127.0.0.1:5123",
		"--pi-listen-addr", "127.0.0.1:5127",
		"--metrics-listen-addr", "127.0.0.1:9090",
		"--registries", "http://registry.example:5000",
		"--group", "test",
	}
	require.NoError(t, parser.Parse(argv))
	require.Equal(t, 12345, args.AdvertiseCacheMaxKeys)
	require.NoError(t, parser.Parse(append(argv, "--advertise-cache-max-keys", "50")))
	require.Equal(t, 50, args.AdvertiseCacheMaxKeys)
}

func TestRefreshIntervalValidation(t *testing.T) {
	const maxMinutes = int64(1<<63-1) / int64(time.Minute)
	for _, tc := range []struct {
		minutes int64
		valid   bool
	}{
		{minutes: 1, valid: true},
		{minutes: 60, valid: true},
		{minutes: maxMinutes, valid: true},
		{minutes: 0},
		{minutes: -1},
		{minutes: maxMinutes + 1},
		{minutes: 1<<63 - 1},
	} {
		t.Run(strconv.FormatInt(tc.minutes, 10), func(t *testing.T) {
			args := parseTestArguments(t, "--full-refresh-minutes", strconv.FormatInt(tc.minutes, 10))
			if tc.valid {
				require.NoError(t, args.validate())
			} else {
				require.ErrorContains(t, args.validate(), "--full-refresh-minutes must be between")
			}
		})
	}
}
