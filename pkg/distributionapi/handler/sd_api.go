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
			Group:  req.Group,
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

// FindKey finds holders for a key
// GET /api/v1/distribution/findkey?key=xxx&count=10&group=xxx
func (h *DistributionHandler) FindKey(c *gin.Context) {
	var req model.FindKeyRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.log.Error(err, "failed to bind query parameters")
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Wrong request format: " + err.Error(),
		})
		return
	}

	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "key is empty!",
		})
		return
	}

	// Get limited distributions if count is specified
	var distributions []*model.Distribution
	var limit int
	if req.Count > 0 {
		limit = req.Count
	}
	distributions, err := h.m.GetHolderByKey(req.Group, req.Key, limit)
	if err != nil {
		h.log.Error(err, "failed to get holders by key with limit", "key", req.Key, "count", req.Count)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Error when finding holders: " + err.Error(),
		})
		return
	}
	holders := make([]string, 0, len(distributions))
	for _, dist := range distributions {
		holders = append(holders, dist.Holder)
	}

	h.log.Info("found holders for key", "group", req.Group, "key", req.Key, "returned", len(holders))
	c.JSON(http.StatusOK, model.FindKeyResponse{
		Key:     req.Key,
		Holders: holders,
		Group:   req.Group,
	})
}

// sync api will delete all the holder's key, and then insert the current keys
// POST /api/v1/distribution/sync
func (h *DistributionHandler) Sync(c *gin.Context) {
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
			Group:  req.Group,
		})
	}

	if len(distributions) == 0 {
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "No operation needed",
		})
		return
	}

	if err := h.m.SyncDistributions(req.Holder, distributions); err != nil {
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
