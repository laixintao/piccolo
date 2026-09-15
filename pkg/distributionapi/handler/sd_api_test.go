package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
			name: "exclude every port before limiting and prefer closest remaining peer",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.72:5127", "10.116.52.77:15127",
				"10.116.52.76:5127", "10.116.52.73:5127",
			},
			requestHost: "10.116.52.77", count: 2, status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127"},
		},
		{
			name: "count one still returns another peer",
			holders: []string{
				"10.116.52.77:5127", "10.116.52.72:5127", "10.116.52.76:5127",
			},
			requestHost: "10.116.52.77", count: 1, status: http.StatusOK,
			want: []string{"10.116.52.76:5127"},
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
			name:        "requester absent still prioritizes closest peer",
			holders:     []string{"10.116.52.72:5127", "10.116.52.76:5127"},
			requestHost: "10.116.52.77", status: http.StatusOK,
			want: []string{"10.116.52.76:5127", "10.116.52.72:5127"},
		},
		{
			name:    "no request host preserves existing behavior",
			holders: []string{"10.116.52.77:5127", "10.116.52.76:5127"},
			count:   1, status: http.StatusOK,
			want: []string{"10.116.52.77:5127"},
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
			require.Equal(t, tt.want, response.Holders)
		})
	}
}

func newFindKeyTestHandler(t *testing.T, holders []string) *DistributionHandler {
	t.Helper()
	// Exercise the real handler and storage query without a MySQL service. DryRun
	// prevents SQL execution; this callback supplies the query result only.
	db, err := gorm.Open(mysql.New(mysql.Config{SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:holders", func(tx *gorm.DB) {
		result, ok := tx.Statement.Dest.(*[]string)
		require.True(t, ok)
		*result = append([]string(nil), holders...)
		tx.RowsAffected = int64(len(holders))
	}))
	return NewDistributionHandler(storage.NewManager(db, nil, nil), logr.Discard())
}
