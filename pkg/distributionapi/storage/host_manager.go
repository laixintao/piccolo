package storage

import (
	"fmt"
	"time"

	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		metrics.DBQueryTotal.WithLabelValues("host_tab", "refresh_host_addr").Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "refresh_host_addr").Observe(time.Since(start).Seconds())
	}()

	now := time.Now()
	host := &model.Host{
		HostAddr: hostAddr,
		Group:    group,
		LastSeen: now,
	}

	return m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "host_addr"}, {Name: "group"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_seen"}),
	}).Create(host).Error
}

func (m *HostManager) FindDeadHosts() ([]model.Host, error) {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("host_tab", "find_dead_hosts").Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "find_dead_hosts").Observe(time.Since(start).Seconds())
	}()

	threshold := time.Now().Add(-DEADTIMEOUT)
	var deadHosts []model.Host
	if err := m.db.
		Model(&model.Host{}).
		Where("last_seen < ?", threshold).
		Find(&deadHosts).Error; err != nil {
		return nil, fmt.Errorf("failed to find dead hosts: %w", err)
	}

	return deadHosts, nil
}

func (m *HostManager) DeleteHost(host model.Host) error {
	start := time.Now()
	defer func() {
		metrics.DBQueryTotal.WithLabelValues("distribution_tab", "delete_host").Inc()
		metrics.DBQueryDuration.WithLabelValues("distribution_tab", "delete_host").Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Where("`host_addr` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Host{}).Error; err != nil {
		return fmt.Errorf("failed to delete host %s (group=%s): %w",
			host.HostAddr, host.Group, err)
	}
	return nil
}
