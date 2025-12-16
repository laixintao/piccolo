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
	now := time.Now()
	host := &model.Host{
		HostAddr: hostAddr,
		Group:    group,
		LastSeen: now,
	}

	err := m.db.Clauses(
		dbresolver.Use(group),
		dbresolver.Write,
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "host_addr"}, {Name: "group"}},
			DoUpdates: clause.AssignmentColumns([]string{"last_seen"}),
		},
	).Create(host).Error

	status := "success"
	if err != nil {
		status = "fail"
	}
	metrics.DBQueryTotal.WithLabelValues("host_tab", "refresh_host_addr", group, status).Inc()
	metrics.DBQueryDuration.WithLabelValues("host_tab", "refresh_host_addr", group, status).Observe(time.Since(start).Seconds())
	return err
}

func (m *HostManager) FindDeadHosts(group string) ([]model.Host, error) {
	start := time.Now()
	var retErr error
	defer func() {
		status := "success"
		if retErr != nil {
			status = "fail"
		}
		metrics.DBQueryTotal.WithLabelValues("host_tab", "find_dead_hosts", group, status).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "find_dead_hosts", group, status).Observe(time.Since(start).Seconds())
	}()

	threshold := time.Now().Add(-DEADTIMEOUT)
	var deadHosts []model.Host
	if err := m.db.
		Clauses(dbresolver.Use(group)).
		Model(&model.Host{}).
		Where("last_seen < ? AND `group` = ?", threshold, group).
		Find(&deadHosts).Error; err != nil {
		retErr = fmt.Errorf("failed to find dead hosts: %w", err)
		return nil, retErr
	}

	return deadHosts, nil
}

// FindDeadHostsByMasterResolver finds all dead hosts in a specific master database
// This is used by evictor to clean up hosts regardless of their group field value
func (m *HostManager) FindDeadHostsByMasterResolver(masterResolver string) ([]model.Host, error) {
	start := time.Now()
	var retErr error
	defer func() {
		status := "success"
		if retErr != nil {
			status = "fail"
		}
		metrics.DBQueryTotal.WithLabelValues("host_tab", "find_dead_hosts_by_master", masterResolver, status).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "find_dead_hosts_by_master", masterResolver, status).Observe(time.Since(start).Seconds())
	}()

	threshold := time.Now().Add(-DEADTIMEOUT)
	var deadHosts []model.Host
	if err := m.db.
		Clauses(dbresolver.Use(masterResolver), dbresolver.Write).
		Model(&model.Host{}).
		Where("last_seen < ?", threshold).
		Find(&deadHosts).Error; err != nil {
		retErr = fmt.Errorf("failed to find dead hosts from master resolver %s: %w", masterResolver, err)
		return nil, retErr
	}

	return deadHosts, nil
}

func (m *HostManager) DeleteHost(host model.Host) error {
	start := time.Now()
	var retErr error
	defer func() {
		status := "success"
		if retErr != nil {
			status = "fail"
		}
		metrics.DBQueryTotal.WithLabelValues("host_tab", "delete_host", host.Group, status).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "delete_host", host.Group, status).Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Clauses(dbresolver.Use(host.Group), dbresolver.Write).
		Where("`host_addr` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Host{}).Error; err != nil {
		retErr = fmt.Errorf("failed to delete host %s (group=%s): %w",
			host.HostAddr, host.Group, err)
		return retErr
	}
	return nil
}

// DeleteHostByMasterResolver deletes a host using the master resolver
// This ensures deletion from the correct physical database
func (m *HostManager) DeleteHostByMasterResolver(host model.Host, masterResolver string) error {
	start := time.Now()
	var retErr error
	defer func() {
		status := "success"
		if retErr != nil {
			status = "fail"
		}
		metrics.DBQueryTotal.WithLabelValues("host_tab", "delete_host_by_master", masterResolver, status).Inc()
		metrics.DBQueryDuration.WithLabelValues("host_tab", "delete_host_by_master", masterResolver, status).Observe(time.Since(start).Seconds())
	}()

	if err := m.db.
		Clauses(dbresolver.Use(masterResolver), dbresolver.Write).
		Where("`host_addr` = ? AND `group` = ?", host.HostAddr, host.Group).
		Delete(&model.Host{}).Error; err != nil {
		retErr = fmt.Errorf("failed to delete host %s (group=%s) from master %s: %w",
			host.HostAddr, host.Group, masterResolver, err)
		return retErr
	}
	return nil
}
