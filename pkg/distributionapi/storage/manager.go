package storage

import (
	"fmt"

	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxBatch = 1000

type DistributionManagerInterface interface {
	CreateDistributions(distributions []*model.Distribution) error
	GetHolderByKey(key string, limit int) ([]*model.Distribution, error)
	Close() error
}

type DistributionManager struct {
	db *gorm.DB
}

func NewDistributionManager(db *gorm.DB) *DistributionManager {
	return &DistributionManager{
		db: db,
	}
}

func (m *DistributionManager) CreateDistributions(distributions []*model.Distribution) error {
	if len(distributions) == 0 {
		return nil
	}

	m.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(distributions, MaxBatch)
	return nil
}

func (m *DistributionManager) SyncDistributions(holder string, distributions []*model.Distribution) error {
	if len(distributions) == 0 {
		return nil
	}
	return m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("holder = ?", holder).Delete(&model.Distribution{}).Error; err != nil {
			return err
		}

		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(distributions, MaxBatch).Error; err != nil {
			return err
		}

		return nil
	})
}

func (m *DistributionManager) GetHolderByKey(key string, limit int) ([]*model.Distribution, error) {
	var distributions []*model.Distribution

	query := m.db.Where("`key` = ?", key)
	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Find(&distributions).Error; err != nil {
		return nil, fmt.Errorf("failed to get holders by key %s: %w", key, err)
	}

	return distributions, nil
}

func (m *DistributionManager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	return nil
}
