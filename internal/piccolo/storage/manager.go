package storage

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/laixintao/piccolo/internal/piccolo/model"
	"gorm.io/gorm"
)

type DistributionManagerInterface interface {
	CreateDistributions(distributions []*model.Distribution) error
	GetHolderByKey(key string) ([]*model.Distribution, error)
	Close() error
}

type DistributionManager struct {
	db *gorm.DB
}
