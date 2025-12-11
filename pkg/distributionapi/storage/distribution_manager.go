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

func (m *DistributionManager) CreateDistributions(distributions []*model.Distribution, group string) error {
	if len(distributions) == 0 {
		return nil
	}

	start := time.Now()
	if err := m.db.Clauses(
		clause.Insert{Modifier: "IGNORE"},
		dbresolver.Use(group),
	).CreateInBatches(distributions, MaxBatch).Error; err != nil {
		return err
	}
	metrics.DBQueryTotal.WithLabelValues("distribution_tab", "insert", group).Inc()
	metrics.DBQueryDuration.WithLabelValues("distribution_tab", "insert", group).Observe(time.Since(start).Seconds())
	return nil
}

func (m *DistributionManager) GetHolderByKey(ctx context.Context, group string, key string) ([]string, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "get_holder_by_key", group).Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "get_holder_by_key", group).Observe(time.Since(start).Seconds())
	}()

	var holders []string
	query := m.db.WithContext(ctx).
		Clauses(dbresolver.Use(group)).
		Model(&model.Distribution{}).
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
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "get_keys_by_holder", group).Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "get_keys_by_holder", group).Observe(time.Since(start).Seconds())
	}()

	var keys []string
	if err := m.db.
		Clauses(dbresolver.Use(group)).
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
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_by_keys", group).Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_by_keys", group).Observe(time.Since(start).Seconds())
	}()

	return m.db.
		Clauses(dbresolver.Use(group)).
		Where("`group` = ? AND `key` IN ? AND holder = ?", group, keys, holder).
		Delete(&model.Distribution{}).Error
}

func (m *DistributionManager) DeleteByHolder(host model.Host) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_by_holder", host.Group).Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_by_holder", host.Group).Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Clauses(dbresolver.Use(host.Group)).
		Where("`holder` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Distribution{}).Error; err != nil {
		return fmt.Errorf("failed to delete distributions for holder %s (group=%s): %w",
			host.HostAddr, host.Group, err)
	}
	return nil
}

// DeleteByHolderByMasterResolver deletes distributions using the master resolver
// This ensures deletion from the correct physical database
func (m *DistributionManager) DeleteByHolderByMasterResolver(host model.Host, masterResolver string) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_by_holder_by_master", masterResolver).Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_by_holder_by_master", masterResolver).Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Clauses(dbresolver.Use(masterResolver), dbresolver.Write).
		Where("`holder` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Distribution{}).Error; err != nil {
		return fmt.Errorf("failed to delete distributions for holder %s (group=%s) from master %s: %w",
			host.HostAddr, host.Group, masterResolver, err)
	}
	return nil
}
