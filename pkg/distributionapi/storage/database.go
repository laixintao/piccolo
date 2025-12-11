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

func InitMySQL(dsnList []string) (*gorm.DB, error) {

	// key = group, value = {type: dsn}
	dsnConfig := make(map[string]map[string][]string)
	for _, d := range dsnList {
		parts := strings.SplitN(d, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid DSN format: %s, expected format: group:type:dsn", d)
		}
		groupName := parts[0]
		dbType := parts[1]
		dsnString := parts[2]

		if dsnConfig[groupName] == nil {
			dsnConfig[groupName] = make(map[string][]string)
		}
		dsnConfig[groupName][dbType] = append(dsnConfig[groupName][dbType], dsnString)
	}

	// 获取 default master DSN 作为主连接
	defaultMasters, ok := dsnConfig["default"]["master"]
	if !ok || len(defaultMasters) == 0 {
		return nil, fmt.Errorf("You must set default:master:dsn for the default db source!")
	}
	defaultDSN := defaultMasters[0]

	// 初始化 db 连接
	db, err := gorm.Open(mysql.Open(defaultDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// 构建 dbresolver 配置
	var resolver *dbresolver.DBResolver

	// 首先注册 default 组的 slave（如果有）
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

	// 然后注册其他 group 的配置
	for group, typeDSNs := range dsnConfig {
		if group == "default" {
			continue // 已经处理过了
		}

		var sources []gorm.Dialector
		var replicas []gorm.Dialector

		// 收集 master DSN
		if masters, ok := typeDSNs["master"]; ok {
			for _, masterDSN := range masters {
				sources = append(sources, mysql.Open(masterDSN))
			}
		}

		// 收集 slave DSN
		if slaves, ok := typeDSNs["slave"]; ok {
			for _, slaveDSN := range slaves {
				replicas = append(replicas, mysql.Open(slaveDSN))
			}
		}

		// 如果这个 group 有配置，就注册
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

			// 链式调用 Register，用 group 名称作为 resolver 名称
			if resolver == nil {
				resolver = dbresolver.Register(config, group)
			} else {
				resolver = resolver.Register(config, group)
			}
		}
	}

	// 应用 dbresolver
	if resolver != nil {
		if err := db.Use(resolver); err != nil {
			return nil, fmt.Errorf("failed to use dbresolver: %w", err)
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
