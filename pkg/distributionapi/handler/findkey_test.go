package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestFindKeyTotal(t *testing.T) {
	t.Parallel()

	manyHolders := make([]string, 101)
	for i := range manyHolders {
		manyHolders[i] = fmt.Sprintf("10.0.0.%d:5002", i+1)
	}

	for _, tt := range []struct {
		name        string
		holders     []string
		query       string
		wantStatus  int
		wantHolders []string
		wantTotal   int
	}{
		{
			name:        "single holder",
			holders:     []string{"10.0.0.11:5002"},
			wantStatus:  http.StatusOK,
			wantHolders: []string{"10.0.0.11:5002"},
			wantTotal:   1,
		},
		{
			name:        "count limits holders but not total",
			holders:     []string{"10.0.0.11:5002", "10.0.0.12:5002"},
			query:       "&count=1",
			wantStatus:  http.StatusOK,
			wantHolders: []string{"10.0.0.11:5002"},
			wantTotal:   2,
		},
		{
			name:        "default limit preserves total",
			holders:     manyHolders,
			wantStatus:  http.StatusOK,
			wantHolders: manyHolders[:100],
			wantTotal:   101,
		},
		{
			name:       "no holders remains not found",
			wantStatus: http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Supply query results without connecting to a MySQL server.
			db, err := gorm.Open(mysql.New(mysql.Config{
				DSN:                       "piccolo@tcp(127.0.0.1:3306)/piccolo",
				SkipInitializeWithVersion: true,
			}), &gorm.Config{
				DryRun:                 true,
				DisableAutomaticPing:   true,
				SkipDefaultTransaction: true,
				Logger:                 logger.Default.LogMode(logger.Silent),
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var queries int
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:holders", func(tx *gorm.DB) {
				queries++
				*tx.Statement.Dest.(*[]string) = tt.holders
			}))

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/distribution/findkey?key=test-key&group=default"+tt.query, nil)
			handler := NewDistributionHandler(storage.NewManager(db, nil, nil), logr.Discard())
			handler.FindKey(ctx)

			require.Equal(t, 1, queries, "total must not require a separate count query")
			require.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantStatus == http.StatusNotFound {
				require.JSONEq(t, `{"message":"Didn't find the key test-key in piccolo"}`, recorder.Body.String())
				return
			}
			var response model.FindKeyResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, "test-key", response.Key)
			require.Equal(t, "default", response.Group)
			require.Equal(t, tt.wantHolders, response.Holders)
			require.Equal(t, tt.wantTotal, response.Total)
		})
	}
}
