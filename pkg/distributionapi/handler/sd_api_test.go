package handler

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testNoopDriverName = "piccolo-noop-sql"

var registerTestNoopDriver sync.Once

func TestFindKeyExcludesRequesterIP(t *testing.T) {
	for _, tt := range []struct {
		name        string
		holders     []string
		requestHost string
		count       int
		status      int
		want        []string
	}{
		{
			name: "reported self download",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.76:5127", "10.116.52.72:5127",
				"10.116.52.73:5127", "10.116.52.74:5127",
			},
			requestHost: "10.116.52.77", count: 5, status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127", "10.116.52.73:5127", "10.116.52.74:5127"},
		},
		{
			name: "exclude every port before limiting",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.72:5127", "10.116.52.77:15127",
				"10.116.52.76:5127", "10.116.52.73:5127",
			},
			requestHost: "10.116.52.77", count: 2, status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127", "10.116.52.73:5127"},
		},
		{
			name: "count one still returns another peer",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.72:5127", "10.116.52.76:5127",
			},
			requestHost: "10.116.52.77", count: 1, status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127"},
		},
		{
			name: "only requester is a miss",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.77:15127",
			},
			requestHost: "10.116.52.77", count: 5, status: http.StatusNotFound,
		},
		{
			name: "IPv4 mapped holder cannot bypass exclusion",
			holders: []string{
				"[::ffff:10.116.52.77]:5127", "10.116.52.76:5127",
			},
			requestHost: "10.116.52.77", count: 5, status: http.StatusOK,
			want: []string{"10.116.52.76:5127"},
		},
		{
			name:        "IPv4 mapped requester",
			holders:     []string{"10.116.52.77:5127", "10.116.52.76:5127"},
			requestHost: "::ffff:10.116.52.77", count: 5, status: http.StatusOK,
			want: []string{"10.116.52.76:5127"},
		},
		{
			name:        "requester absent samples remaining peers",
			holders:     []string{"10.116.52.72:5127", "10.116.52.76:5127"},
			requestHost: "10.116.52.77", status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127"},
		},
		{
			name:    "no request host still samples peers",
			holders: []string{"10.116.52.77:5127", "10.116.52.76:5127"},
			count:   1, status: http.StatusOK,
			want: []string{"10.116.52.77:5127", "10.116.52.76:5127"},
		},
		{
			name:        "missing key remains a miss",
			requestHost: "10.116.52.77", count: 5, status: http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := newFindKeyTestHandler(t, tt.holders)
			params := url.Values{
				"key":   {"sha256:4aedd3f949434505a7e7e8456bfc5ac944b220c6f15fc5675b8a4335532efa4c"},
				"group": {"ap-sg-1-general-d"},
				"count": {strconv.Itoa(tt.count)},
			}
			if tt.requestHost != "" {
				params.Set("request_host", tt.requestHost)
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/distribution/findkey?"+params.Encode(), nil)
			handler.FindKey(ctx)

			require.Equal(t, tt.status, recorder.Code, recorder.Body.String())
			if tt.status == http.StatusNotFound {
				var response map[string]string
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.Contains(t, response["message"], params.Get("key"))
				return
			}
			var response model.FindKeyResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, params.Get("key"), response.Key)
			require.Equal(t, params.Get("group"), response.Group)
			limit := tt.count
			if limit <= 0 {
				limit = 100
			}
			require.Len(t, response.Holders, min(limit, len(tt.want)))
			require.Subset(t, tt.want, response.Holders)
			require.Len(t, uniqueHolders(response.Holders), len(response.Holders))
		})
	}
}

func TestAdvertiseImageInvalidatesTouchedPeerCacheEntries(t *testing.T) {
	handler := newDistributionHandlerForWriteTests(t, func(tx *gorm.DB) {})
	group := "ap-sg-1-general-d"
	staleKey := "sha256:stale"
	keepKey := "sha256:keep"
	cacheUntil := time.Now().Add(time.Minute)
	handler.peers.store(peerWindow{
		key:       peerCacheKey{group, staleKey},
		holders:   []string{"stale:5127"},
		refreshAt: cacheUntil,
	})
	handler.peers.store(peerWindow{
		key:       peerCacheKey{group, keepKey},
		holders:   []string{"keep:5127"},
		refreshAt: cacheUntil,
	})

	body := `{"holder":"10.0.0.1:5127","group":"` + group + `","keys":["` + staleKey + `","", "` + staleKey + `"]}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/distribution/advertise", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.AdvertiseImage(ctx)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.NotContains(t, handler.peers.entries, peerCacheKey{group, staleKey})
	require.Contains(t, handler.peers.entries, peerCacheKey{group, keepKey})
}

func TestSyncInvalidatesAddedAndRemovedPeerCacheEntries(t *testing.T) {
	group := "ap-sg-1-general-d"
	holder := "10.0.0.1:5127"
	removedKey := "sha256:removed"
	unchangedKey := "sha256:unchanged"
	addedKey := "sha256:added"
	handler := newDistributionHandlerForWriteTests(t, func(tx *gorm.DB) {
		if keys, ok := tx.Statement.Dest.(*[]string); ok {
			*keys = []string{removedKey, unchangedKey}
		}
	})
	cacheUntil := time.Now().Add(time.Minute)
	for _, entry := range []peerWindow{
		{key: peerCacheKey{group, removedKey}, holders: []string{"removed:5127"}, refreshAt: cacheUntil},
		{key: peerCacheKey{group, unchangedKey}, holders: []string{"unchanged:5127"}, refreshAt: cacheUntil},
		{key: peerCacheKey{group, addedKey}, holders: []string{"added:5127"}, refreshAt: cacheUntil},
	} {
		handler.peers.store(entry)
	}

	body := `{"holder":"` + holder + `","group":"` + group + `","keys":["` + unchangedKey + `","` + addedKey + `"]}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/distribution/sync", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.Sync(ctx)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.NotContains(t, handler.peers.entries, peerCacheKey{group, removedKey})
	require.Contains(t, handler.peers.entries, peerCacheKey{group, unchangedKey})
	require.NotContains(t, handler.peers.entries, peerCacheKey{group, addedKey})
}

func newFindKeyTestHandler(t *testing.T, holders []string) *DistributionHandler {
	return newDistributionHandlerForWriteTests(t, func(tx *gorm.DB) {
		result, ok := tx.Statement.Dest.(*[]string)
		require.True(t, ok)
		*result = append([]string(nil), holders...)
		tx.RowsAffected = int64(len(holders))
	})
}

func newDistributionHandlerForWriteTests(t *testing.T, query func(*gorm.DB)) *DistributionHandler {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      newNoopSQLDB(t),
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:holders", query))
	handler, err := NewDistributionHandler(storage.NewManager(db, nil, nil), logr.Discard(), DefaultPeerCacheConfig())
	require.NoError(t, err)
	handler.randomIntN = rand.New(rand.NewPCG(1, 2)).IntN
	return handler
}

func newNoopSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	registerTestNoopDriver.Do(func() { sql.Register(testNoopDriverName, noopDriver{}) })
	db, err := sql.Open(testNoopDriverName, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

type noopDriver struct{}

func (noopDriver) Open(string) (driver.Conn, error) { return noopConn{}, nil }

type noopConn struct{}

func (noopConn) Prepare(string) (driver.Stmt, error) { return noopStmt{}, nil }
func (noopConn) Close() error                        { return nil }
func (noopConn) Begin() (driver.Tx, error)           { return noopTx{}, nil }
func (noopConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return noopStmt{}, nil
}
func (noopConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return noopTx{}, nil
}
func (noopConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return noopResult(0), nil
}
func (noopConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return noopRows{}, nil
}
func (noopConn) CheckNamedValue(*driver.NamedValue) error { return nil }

type noopStmt struct{}

func (noopStmt) Close() error                               { return nil }
func (noopStmt) NumInput() int                              { return -1 }
func (noopStmt) Exec([]driver.Value) (driver.Result, error) { return noopResult(0), nil }
func (noopStmt) Query([]driver.Value) (driver.Rows, error)  { return noopRows{}, nil }
func (noopStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return noopResult(0), nil
}
func (noopStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return noopRows{}, nil
}

type noopTx struct{}

func (noopTx) Commit() error   { return nil }
func (noopTx) Rollback() error { return nil }

type noopResult int64

func (r noopResult) LastInsertId() (int64, error) { return 0, nil }
func (r noopResult) RowsAffected() (int64, error) { return int64(r), nil }

type noopRows struct{}

func (noopRows) Columns() []string         { return []string{"value"} }
func (noopRows) Close() error              { return nil }
func (noopRows) Next([]driver.Value) error { return io.EOF }

func uniqueHolders(holders []string) map[string]bool {
	unique := make(map[string]bool, len(holders))
	for _, holder := range holders {
		unique[holder] = true
	}
	return unique
}

func TestFindKeyDistributesFirstAttemptAndFallbacks(t *testing.T) {
	for _, requester := range []string{"", "10.0.0.1"} {
		t.Run("requester="+requester, func(t *testing.T) {
			holders := make([]string, 20)
			for i := range holders {
				holders[i] = fmt.Sprintf("10.0.0.%d:5127", i+1)
			}
			handler := newFindKeyTestHandler(t, holders)
			first := make(map[string]int)
			selected := make(map[string]int)
			for i := 0; i < 200; i++ {
				recorder := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(recorder)
				ctx.Request = httptest.NewRequest(http.MethodGet,
					"/api/v1/distribution/findkey?key=hot&group=test&count=5&request_host="+requester, nil)
				handler.FindKey(ctx)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				var response model.FindKeyResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.Len(t, response.Holders, 5)
				require.Len(t, uniqueHolders(response.Holders), 5)
				if requester != "" {
					require.NotContains(t, response.Holders, requester+":5127")
				}
				first[response.Holders[0]]++
				for _, holder := range response.Holders {
					selected[holder]++
				}
			}
			eligible := len(holders)
			if requester != "" {
				eligible--
			}
			// The seeded sequence exercises the real handler, including repeated
			// cache hits. The nearest peer must not monopolize the first attempt.
			require.Len(t, first, eligible)
			require.Len(t, selected, eligible)
			for _, count := range first {
				require.Less(t, count, 40)
			}
		})
	}
}
