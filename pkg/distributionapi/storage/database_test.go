package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitMySQLRejectsInvalidConfigurationBeforeConnecting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{name: "format", config: "invalid", expected: "invalid DSN format: expected group:role:dsn"},
		{name: "group", config: ":master:dsn", expected: "database group cannot be empty"},
		{name: "role", config: "default:writer:dsn", expected: `invalid database role "writer": expected master or slave`},
		{name: "DSN", config: "default:master:", expected: `database DSN cannot be empty for group "default"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := InitMySQL([]string{tt.config})
			require.EqualError(t, err, tt.expected)
		})
	}
}
