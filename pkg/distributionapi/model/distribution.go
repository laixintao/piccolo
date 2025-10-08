package model

import (
	"time"
)

type Distribution struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Key       string    `gorm:"size:255;uniqueIndex:idx_key_holder_uniq,priority:1" json:"key"`
	Holder    string    `gorm:"size:64;uniqueIndex:idx_key_holder_uniq,priority:2;index:idx_holder" json:"holder"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Distribution) TableName() string {
	return "distribution_tab"
}

type ImageAdvertiseRequest struct {
	Holder string   `json:"holder" binding:"required"`
	Keys   []string `json:"keys" binding:"required"`
}

type ImageAdvertiseResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type FindKeyRequest struct {
	Key   string `form:"key" binding:"required"`
	Count int    `form:"count"`
}

type FindKeyResponse struct {
	Key     string   `json:"key"`
	Holders []string `json:"holders"`
	Total   int      `json:"total"`
}
