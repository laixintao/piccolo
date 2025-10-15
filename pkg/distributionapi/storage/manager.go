package storage

import (
	"fmt"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxBatch = 100

type DistributionManagerInterface interface {
	CreateDistributions(distributions []*model.Distribution) error
	GetHolderByKey(key string, limit int) ([]string, error)
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

	for _, d := range distributions {
		start := time.Now()
		if err := m.db.Clauses(clause.Insert{Modifier: "IGNORE"}).CreateInBatches(d, MaxBatch).Error; err != nil {
			return err
		}
		metrics.DBQueryTotal.WithLabelValues("insert").Inc()
		metrics.DBQueryDuration.WithLabelValues("insert").Observe(time.Since(start).Seconds())
	}
	return nil
}

func (m *DistributionManager) GetHolderByKey(group string, key string, limit int) ([]string, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("get_holder_by_key").Inc()
		metrics.DBQueryDuration.WithLabelValues("get_holder_by_key").Observe(time.Since(start).Seconds())
	}()

	var holders []string
	query := m.db.Model(&model.Distribution{}).
		Where("`key` = ? AND `group` = ?", key, group)

	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Pluck("holder", &holders).Error; err != nil {
		return nil, fmt.Errorf("failed to get holders by key %s: %w", key, err)
	}

	return holders, nil
}

func (m *DistributionManager) GetKeysByHolder(holder string) ([]string, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("get_keys_by_holder").Inc()
		metrics.DBQueryDuration.WithLabelValues("get_keys_by_holder").Observe(time.Since(start).Seconds())
	}()

	var keys []string
	if err := m.db.
		Model(&model.Distribution{}).
		Where("holder = ?", holder).
		Pluck("`key`", &keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

func (m *DistributionManager) DeleteByKeys(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("delete_by_keys").Inc()
		metrics.DBQueryDuration.WithLabelValues("delete_by_keys").Observe(time.Since(start).Seconds())
	}()

	return m.db.
		Where("`key` IN ?", keys).
		Delete(&model.Distribution{}).Error
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
