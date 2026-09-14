package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSync(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		existingKeys []string
		requestKeys  []string
		queryErr     error
		wantStatus   int
		wantResponse model.ImageAdvertiseResponse
		wantCreates  int
		wantDeletes  int
	}{
		{
			name:         "query failure stops writes",
			requestKeys:  []string{"new"},
			queryErr:     errors.New("database unavailable"),
			wantStatus:   http.StatusInternalServerError,
			wantResponse: model.ImageAdvertiseResponse{Success: false, Message: "Error when querying keys from DB"},
		},
		{
			name:         "query failure with empty request keys",
			requestKeys:  []string{},
			queryErr:     errors.New("database unavailable"),
			wantStatus:   http.StatusInternalServerError,
			wantResponse: model.ImageAdvertiseResponse{Success: false, Message: "Error when querying keys from DB"},
		},
		{
			name:         "successful sync updates keys",
			existingKeys: []string{"old", "shared"},
			requestKeys:  []string{"shared", "new"},
			wantStatus:   http.StatusCreated,
			wantResponse: model.ImageAdvertiseResponse{Success: true, Message: "Distribution created!"},
			wantCreates:  1,
			wantDeletes:  1,
		},
		{
			name:         "successful sync with unchanged keys",
			existingKeys: []string{"shared"},
			requestKeys:  []string{"shared"},
			wantStatus:   http.StatusCreated,
			wantResponse: model.ImageAdvertiseResponse{Success: true, Message: "Distribution created!"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// DryRun and disabled connection initialization keep this handler test
			// independent of MySQL; callbacks supply reads and observe writes.
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

			var queries, creates, deletes int
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:query", func(tx *gorm.DB) {
				queries++
				*tx.Statement.Dest.(*[]string) = tt.existingKeys
				if tt.queryErr != nil {
					tx.AddError(tt.queryErr)
				}
			}))
			require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:create", func(tx *gorm.DB) {
				creates++
				distributions := tx.Statement.Dest.([]*model.Distribution)
				require.Len(t, distributions, 1)
				assert.Equal(t, "new", distributions[0].Key)
				assert.Equal(t, "127.0.0.1:8080", distributions[0].Holder)
				assert.Equal(t, "test-group", distributions[0].Group)
			}))
			require.NoError(t, db.Callback().Delete().After("gorm:delete").Register("test:delete", func(tx *gorm.DB) {
				deletes++
				assert.Equal(t, []interface{}{"test-group", "old", "127.0.0.1:8080"}, tx.Statement.Vars)
			}))

			body, err := json.Marshal(model.ImageAdvertiseRequest{
				Holder: "127.0.0.1:8080",
				Keys:   tt.requestKeys,
				Group:  "test-group",
			})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/distribution/sync", strings.NewReader(string(body)))
			ctx.Request.Header.Set("Content-Type", "application/json")
			handler := NewDistributionHandler(storage.NewManager(db, nil, nil), logr.Discard())

			handler.Sync(ctx)

			assert.Equal(t, 1, queries)
			assert.Equal(t, tt.wantCreates, creates, "unexpected database inserts")
			assert.Equal(t, tt.wantDeletes, deletes, "unexpected database deletes")
			assert.Equal(t, tt.wantStatus, recorder.Code)
			var response model.ImageAdvertiseResponse
			// Unmarshal rejects trailing data, including a second success response.
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), "response body: %s", recorder.Body.String())
			assert.Equal(t, tt.wantResponse, response)
		})
	}
}
