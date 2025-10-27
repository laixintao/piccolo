package storage

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	MaxBatch          = 100
	FindKeyMaxResults = 2000
	DEADTIMEOUT = 11 * time.Minute
)

type Manager struct {
	db           *gorm.DB
	Distribution *DistributionManager
	Host         *HostManager
}

func NewManager(db *gorm.DB) *Manager {
	return &Manager{
		Distribution: NewDistributionManager(db),
		Host:         NewHostManager(db),
		db:           db,
	}
}

func (m *Manager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	return nil
}
