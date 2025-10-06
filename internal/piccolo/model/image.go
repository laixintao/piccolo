package model

import (
	"time"

	"github.com/opencontainers/go-digest"
	"gorm.io/gorm"
)

// ImageRecord 表示存储在数据库中的镜像记录
type ImageRecord struct {
	ID         uint          `gorm:"primaryKey" json:"id"`
	UUID       string        `gorm:"uniqueIndex;size:36" json:"uuid"`
	Name       string        `gorm:"size:255;index" json:"name"`
	Registry   string        `gorm:"size:255;index" json:"registry"`
	Repository string        `gorm:"size:255;index" json:"repository"`
	Tag        string        `gorm:"size:100;index" json:"tag"`
	Digest     string        `gorm:"size:255;uniqueIndex" json:"digest"`
	Size       int64         `json:"size"`
	Metadata   string        `gorm:"type:text" json:"metadata"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (ImageRecord) TableName() string {
	return "images"
}

// CreateImageRequest 表示创建镜像的请求结构
type CreateImageRequest struct {
	Name       string        `json:"name" binding:"required"`
	Registry   string        `json:"registry" binding:"required"`
	Repository string        `json:"repository" binding:"required"`
	Tag        string        `json:"tag"`
	Digest     digest.Digest `json:"digest" binding:"required"`
	Size       int64         `json:"size"`
	Metadata   string        `json:"metadata"`
}

// CreateImageResponse 表示创建镜像的响应结构
type CreateImageResponse struct {
	Success bool         `json:"success"`
	Message string       `json:"message"`
	Image   *ImageRecord `json:"image,omitempty"`
}

// ListImagesResponse 表示查询镜像列表的响应结构
type ListImagesResponse struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Images  []*ImageRecord `json:"images,omitempty"`
	Total   int64          `json:"total"`
}
