package storage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxBatch = 100

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

	m.db.Clauses(clause.Insert{Modifier: "IGNORE"}).CreateInBatches(distributions, MaxBatch)
	return nil
}

func generateUpdateKey() string {
	return uuid.NewString()
}

func (m *DistributionManager) SyncDistributions(holder string, distributions []*model.Distribution) error {
	updateKey := generateUpdateKey()

	if len(distributions) == 0 {
		return nil
	}

	for _, d := range distributions {
		d.UpdateKey = updateKey
	}

	if err := m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group"}, {Name: "key"}, {Name: "holder"}},
		DoUpdates: clause.AssignmentColumns([]string{"update_key"}),
	}).CreateInBatches(distributions, MaxBatch).Error; err != nil {
		return err
	}

	if err := m.db.
		Where("holder = ? and update_key != ?", holder, updateKey).
		Delete(&model.Distribution{}).
		Error; err != nil {
		return err
	}

	return nil
}

func (m *DistributionManager) GetHolderByKey(group string, key string, limit int) ([]*model.Distribution, error) {
	var distributions []*model.Distribution

	query := m.db.Where("`key` = ? and `group` = ?", key, group)
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
