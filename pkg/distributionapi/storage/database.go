package storage

import (
	"strings"

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

func InitMySQL(dsnList []string) (*gorm.DB, []string, error) {

	// key = group, value = {type: dsn}
	dsnConfig := make(map[string]map[string][]string)
	for _, d := range dsnList {
		parts := strings.SplitN(d, ":", 3)
		if len(parts) != 3 {
			return nil, nil, fmt.Errorf("invalid DSN format: %s, expected format: group:type:dsn", d)
		}
		groupName := parts[0]
		dbType := parts[1]
		dsnString := parts[2]

		if dsnConfig[groupName] == nil {
			dsnConfig[groupName] = make(map[string][]string)
		}
		dsnConfig[groupName][dbType] = append(dsnConfig[groupName][dbType], dsnString)
	}

	// Collect all groups
	groups := make([]string, 0, len(dsnConfig))
	for group := range dsnConfig {
		groups = append(groups, group)
	}

	// Get default master DSN as the primary connection
	defaultMasters, ok := dsnConfig["default"]["master"]
	if !ok || len(defaultMasters) == 0 {
		return nil, nil, fmt.Errorf("You must set default:master:dsn for the default db source!")
	}
	defaultDSN := defaultMasters[0]

	// Initialize database connection
	db, err := gorm.Open(mysql.Open(defaultDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Build dbresolver configuration
	var resolver *dbresolver.DBResolver

	// First, register slaves for the default group (if any)
	if defaultSlaves, ok := dsnConfig["default"]["slave"]; ok && len(defaultSlaves) > 0 {
		var replicas []gorm.Dialector
		for _, slaveDSN := range defaultSlaves {
			replicas = append(replicas, mysql.Open(slaveDSN))
		}
		resolver = dbresolver.Register(dbresolver.Config{
			Replicas:          replicas,
			Policy:            dbresolver.RandomPolicy{},
			TraceResolverMode: true,
		})
	}

	// Then register configurations for other groups
	for group, typeDSNs := range dsnConfig {
		if group == "default" {
			continue // Already handled
		}

		var sources []gorm.Dialector
		var replicas []gorm.Dialector

		// Collect master DSNs
		if masters, ok := typeDSNs["master"]; ok {
			for _, masterDSN := range masters {
				sources = append(sources, mysql.Open(masterDSN))
			}
		}

		// Collect slave DSNs
		if slaves, ok := typeDSNs["slave"]; ok {
			for _, slaveDSN := range slaves {
				replicas = append(replicas, mysql.Open(slaveDSN))
			}
		}

		// Register if this group has configuration
		if len(sources) > 0 || len(replicas) > 0 {
			config := dbresolver.Config{
				Policy:            dbresolver.RandomPolicy{},
				TraceResolverMode: true,
			}

			if len(sources) > 0 {
				config.Sources = sources
			}
			if len(replicas) > 0 {
				config.Replicas = replicas
			}

			// Chain Register calls, using group name as resolver name
			if resolver == nil {
				resolver = dbresolver.Register(config, group)
			} else {
				resolver = resolver.Register(config, group)
			}
		}
	}

	// Apply dbresolver
	if resolver != nil {
		if err := db.Use(resolver); err != nil {
			return nil, nil, fmt.Errorf("failed to use dbresolver: %w", err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	sqlDB.SetMaxIdleConns(MaxIdleConns)
	sqlDB.SetMaxOpenConns(MaxOpenConns)
	sqlDB.SetConnMaxLifetime(ConnMaxLifetime)

	if err := sqlDB.Ping(); err != nil {
		return nil, nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, groups, nil
}

func AutoMigrate(db *gorm.DB, models ...interface{}) error {
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("failed to auto migrate database: %w", err)
	}
	return nil
}
