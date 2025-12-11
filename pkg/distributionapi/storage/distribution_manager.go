package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

type DistributionManager struct {
	db *gorm.DB
}

func NewDistributionManager(db *gorm.DB) *DistributionManager {
	return &DistributionManager{db: db}
}

func (m *DistributionManager) CreateDistributions(distributions []*model.Distribution) error {
	if len(distributions) == 0 {
		return nil
	}

	start := time.Now()
	if err := m.db.Clauses(
		clause.Insert{Modifier: "IGNORE"},
		dbresolver.Use("group1"),
	).CreateInBatches(distributions, MaxBatch).Error; err != nil {
		return err
	}
	metrics.DBQueryTotal.WithLabelValues("distribution_tab", "insert").Inc()
	metrics.DBQueryDuration.WithLabelValues("distribution_tab", "insert").Observe(time.Since(start).Seconds())
	return nil
}

func (m *DistributionManager) GetHolderByKey(ctx context.Context, group string, key string) ([]string, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "get_holder_by_key").Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "get_holder_by_key").Observe(time.Since(start).Seconds())
	}()

	var holders []string
	query := m.db.WithContext(ctx).Model(&model.Distribution{}).
		Where("`group` = ? AND `key` = ?", group, key).
		Limit(FindKeyMaxResults)

	if err := query.Pluck("holder", &holders).Error; err != nil {
		return nil, fmt.Errorf("failed to get holders by key %s: %w", key, err)
	}

	return holders, nil
}

func (m *DistributionManager) GetKeysByHolder(group, holder string) ([]string, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "get_keys_by_holder").Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "get_keys_by_holder").Observe(time.Since(start).Seconds())
	}()

	var keys []string
	if err := m.db.
		Model(&model.Distribution{}).
		Where("`holder` = ? AND `group` = ?", holder, group).
		Pluck("`key`", &keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

func (m *DistributionManager) DeleteByKeysByHolder(keys []string, holder, group string) error {
	if len(keys) == 0 {
		return nil
	}

	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_by_keys").Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_by_keys").Observe(time.Since(start).Seconds())
	}()

	return m.db.
		Where("`group` = ? AND `key` IN ? AND holder = ?", group, keys, holder).
		Delete(&model.Distribution{}).Error
}

func (m *DistributionManager) DeleteByHolder(host model.Host) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_by_holder").Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_by_holder").Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Where("`holder` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Distribution{}).Error; err != nil {
		return fmt.Errorf("failed to delete distributions for holder %s (group=%s): %w",
			host.HostAddr, host.Group, err)
	}
	return nil
}
