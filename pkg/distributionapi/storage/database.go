package storage

import (
	"gorm.io/plugin/prometheus"

	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

const (
	MaxIdleConns    = 10
	MaxOpenConns    = 100
	ConnMaxLifetime = time.Hour
	LogLevel        = logger.Info
)

func InitMySQL(dbMaster string, dbSlaves []string) (*gorm.DB, error) {

	db, err := gorm.Open(mysql.Open(dbMaster), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Use(prometheus.New(prometheus.Config{
		DBName:          "masterDB",
		RefreshInterval: 15,
		MetricsCollector: []prometheus.MetricsCollector{
			&prometheus.MySQL{},
		},
	})); err != nil {
		return nil, fmt.Errorf("failed to initialize prometheus plugin: %w", err)
	}

	// enable read/write split
	if len(dbSlaves) > 0 {
		var replicas []gorm.Dialector
		for _, dsn := range dbSlaves {
			replicas = append(replicas, mysql.Open(dsn))
		}
		// setup read replicas
		if err := db.Use(dbresolver.Register(dbresolver.Config{
			TraceResolverMode: true,
			Policy:            dbresolver.RandomPolicy{},
			Replicas:          replicas,
		})); err != nil {
			return nil, fmt.Errorf("failed to register dbresolver: %w", err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	sqlDB.SetMaxIdleConns(MaxIdleConns)
	sqlDB.SetMaxOpenConns(MaxOpenConns)
	sqlDB.SetConnMaxLifetime(ConnMaxLifetime)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

func AutoMigrate(db *gorm.DB, models ...interface{}) error {
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("failed to auto migrate database: %w", err)
	}
	return nil
}
