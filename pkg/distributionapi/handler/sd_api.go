package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
)

type DistributionHandler struct {
	m   *storage.DistributionManager
	log logr.Logger
}

func NewDistributionHandler(m *storage.DistributionManager, log logr.Logger) *DistributionHandler {
	return &DistributionHandler{
		m:   m,
		log: log,
	}
}

// AdvertiseImage hanle advertise request
// POST /api/v1/distribution/advertise
func (h *DistributionHandler) AdvertiseImage(c *gin.Context) {
	var req model.ImageAdvertiseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "failed to bind JSON request")
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Wrong reuqest format: " + err.Error(),
		})
		return
	}

	if req.Holder == "" {
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "holder is empty!",
		})
		return
	}

	distributions := make([]*model.Distribution, 0, len(req.Keys))
	for _, key := range req.Keys {
		if key == "" {
			continue
		}
		distributions = append(distributions, &model.Distribution{
			Key:    key,
			Holder: req.Holder,
		})
	}

	if len(distributions) == 0 {
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "No operation needed",
		})
		return
	}

	if err := h.m.CreateDistributions(distributions); err != nil {
		h.log.Error(err, "failed to create distributions", "holder", req.Holder, "count", len(distributions))
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when create distribution in batch" + err.Error(),
		})
		return
	}

	h.log.Info("distributions created successfully", "holder", req.Holder, "count", len(distributions))
	c.JSON(http.StatusCreated, model.ImageAdvertiseResponse{
		Success: true,
		Message: "Distribution created!",
	})
}
