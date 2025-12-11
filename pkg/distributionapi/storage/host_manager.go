package storage

import (
	"fmt"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/plugin/dbresolver"
)

type HostManager struct {
	db *gorm.DB
}

func NewHostManager(db *gorm.DB) *HostManager {
	return &HostManager{db: db}
}
func (m *HostManager) RefreshHostAddr(hostAddr, group string) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("host_tab", "refresh_host_addr", group).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "refresh_host_addr", group).Observe(time.Since(start).Seconds())
	}()

	now := time.Now()
	host := &model.Host{
		HostAddr: hostAddr,
		Group:    group,
		LastSeen: now,
	}

	return m.db.Clauses(
		dbresolver.Use(group),
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "host_addr"}, {Name: "group"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_seen"}),
		},
	).Create(host).Error
}

func (m *HostManager) FindDeadHosts(group string) ([]model.Host, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("host_tab", "find_dead_hosts", group).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "find_dead_hosts", group).Observe(time.Since(start).Seconds())
	}()

	threshold := time.Now().Add(-DEADTIMEOUT)
	var deadHosts []model.Host
	if err := m.db.
		Clauses(dbresolver.Use(group)).
		Model(&model.Host{}).
		Where("last_seen < ? AND `group` = ?", threshold, group).
		Find(&deadHosts).Error; err != nil {
		return nil, fmt.Errorf("failed to find dead hosts: %w", err)
	}

	return deadHosts, nil
}

func (m *HostManager) DeleteHost(host model.Host) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("host_tab", "delete_host", host.Group).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "delete_host", host.Group).Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Clauses(dbresolver.Use(host.Group)).
		Where("`host_addr` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Host{}).Error; err != nil {
		return fmt.Errorf("failed to delete host %s (group=%s): %w",
			host.HostAddr, host.Group, err)
	}
	return nil
}
