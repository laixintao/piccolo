package storage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/laixintao/piccolo/internal/piccolo/model"
	"github.com/laixintao/piccolo/pkg/oci"
	"gorm.io/gorm"
)

type GormImageStore struct {
	db *gorm.DB
}

func NewGormImageStore(db *gorm.DB) (*GormImageStore, error) {
	if err := db.AutoMigrate(&model.Distribution{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &GormImageStore{db: db}, nil
}

func (s *GormImageStore) CreateImage(image *model.ImageRecord) error {
	if image.UUID == "" {
		image.UUID = uuid.New().String()
	}

	result := s.db.Create(image)
	return result.Error
}

func (s *GormImageStore) GetImage(uuid string) (*model.ImageRecord, error) {
	var image model.ImageRecord
	result := s.db.Where("uuid = ?", uuid).First(&image)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("image with uuid %s not found", uuid)
		}
		return nil, result.Error
	}
	return &image, nil
}

func (s *GormImageStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
