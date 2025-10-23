package model

import (
	"time"
)

type Distribution struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Key       string    `gorm:"size:255;uniqueIndex:idx_group_key_holder_uniq,priority:2" json:"key"`
	Holder    string    `gorm:"size:64;uniqueIndex:idx_group_key_holder_uniq,priority:3;index:idx_holder" json:"holder"`
	Group     string    `gorm:"size:64;uniqueIndex:idx_group_key_holder_uniq,priority:1" json:"group"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Distribution) TableName() string {
	return "distribution_tab"
}

type Host struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	HostAddr  string    `gorm:"size:64;uniqueIndex:idx_group_host,priority:2" json:"host_addr"`
	Group     string    `gorm:"size:64;uniqueIndex:idx_group_host,priority:1" json:"group"`
	LastSeen  time.Time `json:"last_seen"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Host) TableName() string {
	return "host_tab"
}

type ImageAdvertiseRequest struct {
	Holder string   `json:"holder" binding:"required"`
	Keys   []string `json:"keys" binding:"required"`
	Group  string   `json:"group" binding:"required"`
}

type ImageAdvertiseResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type KeepAliveResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type FindKeyRequest struct {
	Key         string `form:"key" binding:"required"`
	Group       string `form:"group" binding:"required"`
	Count       int    `form:"count"`
	RequestHost string `form:"request_host"`
}

type FindKeyResponse struct {
	Key     string   `json:"key"`
	Group   string   `form:"group" binding:"required"`
	Holders []string `json:"holders"`
	Total   int      `json:"total"`
}

type KeepAliveRequest struct {
	HostAddr string `json:"host" binding:"required"`
	Group    string `form:"group" binding:"required"`
}
