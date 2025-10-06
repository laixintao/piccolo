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
	if err := db.AutoMigrate(&model.ImageRecord{}); err != nil {
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

func (s *GormImageStore) GetImageByDigest(digest string) (*model.ImageRecord, error) {
	var image model.ImageRecord
	result := s.db.Where("digest = ?", digest).First(&image)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("image with digest %s not found", digest)
		}
		return nil, result.Error
	}
	return &image, nil
}

func (s *GormImageStore) ListImages() ([]*model.ImageRecord, error) {
	var images []*model.ImageRecord
	result := s.db.Order("created_at DESC").Find(&images)
	if result.Error != nil {
		return nil, result.Error
	}
	return images, nil
}

func (s *GormImageStore) ListImagesWithPagination(limit, offset int) ([]*model.ImageRecord, int64, error) {
	var images []*model.ImageRecord
	var total int64

	if err := s.db.Model(&model.ImageRecord{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	result := s.db.Order("created_at DESC").Limit(limit).Offset(offset).Find(&images)
	if result.Error != nil {
		return nil, 0, result.Error
	}

	return images, total, nil
}

func (s *GormImageStore) UpdateImage(image *model.ImageRecord) error {
	result := s.db.Save(image)
	return result.Error
}

func (s *GormImageStore) DeleteImage(uuid string) error {
	result := s.db.Where("uuid = ?", uuid).Delete(&model.ImageRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("image with uuid %s not found", uuid)
	}
	return nil
}

func (s *GormImageStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func FromOCIImage(ociImage oci.Image) *model.ImageRecord {
	return &model.ImageRecord{
		UUID:       uuid.New().String(),
		Name:       ociImage.Name,
		Registry:   ociImage.Registry,
		Repository: ociImage.Repository,
		Tag:        ociImage.Tag,
		Digest:     string(ociImage.Digest),
		Size:       0,
		Metadata:   "",
	}
}
