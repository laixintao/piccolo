package model

import (
	"time"
)

type Distribution struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"size:255;index" json:"key"`
	Holder    string    `gorm:"size:64;index" json:"holder"`
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
