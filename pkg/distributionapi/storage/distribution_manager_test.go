package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGetHolderWindowUsesIndexedCursorAndWrapsWithoutDuplicates(t *testing.T) {
	for _, tt := range []struct {
		name, after string
		limit       int
		pages       [][]string
		want        []string
	}{
		{"first page", "", 2, [][]string{{"10.0.0.10:5127", "10.0.0.2:5127"}}, []string{"10.0.0.10:5127", "10.0.0.2:5127"}},
		{"next page", "b", 2, [][]string{{"c", "d"}}, []string{"c", "d"}},
		{"partial tail fills from head", "c", 3, [][]string{{"d"}, {"a", "b"}}, []string{"d", "a", "b"}},
		{"exact boundary wraps immediately", "d", 2, [][]string{{}, {"a", "b"}}, []string{"a", "b"}},
		{"small set is not duplicated", "a", 5, [][]string{{"b", "c"}, {"a"}}, []string{"b", "c", "a"}},
		{"removed cursor still wraps", "z", 2, [][]string{{}, {"a", "b"}}, []string{"a", "b"}},
		{"empty set", "z", 2, [][]string{{}, {}}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			manager := windowTestManager(t, func(tx *gorm.DB) {
				sql := tx.Statement.SQL.String()
				require.Contains(t, sql, "`group` = ? AND `key` = ?")
				require.Contains(t, sql, "ORDER BY holder ASC LIMIT ?")
				require.NotContains(t, sql, "OFFSET")
				vars := []any{"group", "key"}
				limit := tt.limit
				if calls == 0 && tt.after != "" {
					require.Contains(t, sql, "`holder` > ?")
					vars = append(vars, tt.after)
				} else if calls == 1 {
					require.Contains(t, sql, "`holder` <= ?")
					require.NotContains(t, sql, "`holder` > ?")
					vars = append(vars, tt.after)
					limit -= len(tt.pages[0])
				}
				require.Equal(t, append(vars, limit), tx.Statement.Vars)
				require.Less(t, calls, len(tt.pages))
				*tx.Statement.Dest.(*[]string) = append([]string(nil), tt.pages[calls]...)
				calls++
			})
			window, err := manager.GetHolderWindow(context.Background(), "group", "key", tt.after, tt.limit)
			require.NoError(t, err)
			require.Equal(t, tt.want, window)
			require.Equal(t, len(tt.pages), calls)
		})
	}
}

func TestGetHolderWindowFailsAtomicallyOnWrapQueryError(t *testing.T) {
	queryErr := errors.New("replica unavailable")
	var calls int
	manager := windowTestManager(t, func(tx *gorm.DB) {
		calls++
		if calls == 1 {
			*tx.Statement.Dest.(*[]string) = []string{"tail"}
		} else {
			tx.AddError(queryErr)
		}
	})
	window, err := manager.GetHolderWindow(context.Background(), "group", "key", "cursor", 2)
	require.ErrorIs(t, err, queryErr)
	require.Nil(t, window)
}

func windowTestManager(t *testing.T, query func(*gorm.DB)) *DistributionManager {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:holder_window", query))
	return NewDistributionManager(db)
}
