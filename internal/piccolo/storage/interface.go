package storage

import "github.com/laixintao/piccolo/internal/piccolo/model"

type ImageStore interface {
	CreateImage(image *model.ImageRecord) error
	GetImage(uuid string) (*model.ImageRecord, error)
	GetImageByDigest(digest string) (*model.ImageRecord, error)
	ListImages() ([]*model.ImageRecord, error)
	UpdateImage(image *model.ImageRecord) error
	DeleteImage(uuid string) error
	Close() error
}
