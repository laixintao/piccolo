package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/stretchr/testify/require"
)

func parseTestArguments(t *testing.T, extra ...string) Arguments {
	t.Helper()
	var args Arguments
	parser, err := arg.NewParser(arg.Config{IgnoreEnv: true}, &args)
	require.NoError(t, err)
	command := []string{
		"--registry-listen-addr", "127.0.0.1:5001",
		"--pi-listen-addr", "127.0.0.1:5002",
		"--metrics-listen-addr", "127.0.0.1:9090",
		"--registries", "http://registry.example:5000",
		"--group", "test",
	}
	require.NoError(t, parser.Parse(append(command, extra...)))
	return args
}

func TestDefaultRefreshIntervalCanStartTicker(t *testing.T) {
	args := parseTestArguments(t)
	require.NoError(t, args.validate())
	require.NotPanics(t, func() {
		ticker := time.NewTicker(time.Duration(args.FullRefreshMinutes) * time.Minute)
		ticker.Stop()
	}, "omitting an optional CLI argument must not crash the tracker")
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
