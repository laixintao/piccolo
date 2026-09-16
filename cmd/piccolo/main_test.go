package main

import (
	"testing"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/laixintao/piccolo/pkg/distributionapi/handler"
	"github.com/stretchr/testify/require"
)

func TestPeerCacheConfiguration(t *testing.T) {
	parse := func(useEnv bool, flags ...string) *ServerCmd {
		t.Helper()
		args := &Arguments{}
		parser, err := arg.NewParser(arg.Config{IgnoreEnv: !useEnv}, args)
		require.NoError(t, err)
		err = parser.Parse(append([]string{"server", "--db-dsn-list", "default:master:unused"}, flags...))
		require.NoError(t, err)
		return args.Server
	}
	require.Equal(t, handler.DefaultPeerCacheConfig(), parse(false).peerCacheConfig())
	args := parse(false, "--peer-cache-max-keys", "50", "--peer-cache-max-holders", "10000", "--peer-cache-refresh-interval", "20s")
	require.Equal(t, handler.PeerCacheConfig{MaxKeys: 50, MaxHolders: 10000, RefreshInterval: 20 * time.Second}, args.peerCacheConfig())
	require.NoError(t, args.peerCacheConfig().Validate())
	for _, flag := range []string{"--peer-cache-max-keys", "--peer-cache-max-holders", "--peer-cache-refresh-interval"} {
		for _, value := range []string{"0", "-1"} {
			if flag == "--peer-cache-refresh-interval" {
				value += "s"
			}
			require.ErrorContains(t, parse(false, flag+"="+value).peerCacheConfig().Validate(), flag)
		}
	}
	t.Setenv("PEER_CACHE_MAX_KEYS", "7")
	t.Setenv("PEER_CACHE_MAX_HOLDERS", "20000")
	t.Setenv("PEER_CACHE_REFRESH_INTERVAL", "30s")
	require.Equal(t, handler.PeerCacheConfig{MaxKeys: 7, MaxHolders: 20000, RefreshInterval: 30 * time.Second}, parse(true).peerCacheConfig())
	require.Equal(t, 8, parse(true, "--peer-cache-max-keys", "8").PeerCacheMaxKeys)
}
