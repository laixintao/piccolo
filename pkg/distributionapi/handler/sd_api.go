package handler

import (
	"fmt"
	"net/http"
	"time"

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
			Message: "Wrong request format: " + err.Error(),
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

	// Get limited holders if count is specified
	var holders []string
	var limit int
	if req.Count > 0 {
		limit = req.Count
	}
	holders, err := h.m.GetHolderByKey(req.Group, req.Key, limit)
	if err != nil {
		h.log.Error(err, "failed to get holders by key with limit", "key", req.Key, "count", req.Count)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Error when finding holders: " + err.Error(),
		})
		return
	}
	if len(holders) == 0 {
		c.JSON(http.StatusNotFound,
			gin.H{"message": fmt.Sprintf("Didn't find the key %s in piccolo", req.Key)},
		)
		return
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
	start := time.Now()
	var req model.ImageAdvertiseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "failed to bind JSON request")
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Wrong request format: " + err.Error(),
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

	existingKeys, err := h.m.GetKeysByHolder(req.Holder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when delete keys from DB",
		})
	}

	currentKeys := req.Keys

	onlyInDB, onlyInRequest := diffSets(existingKeys, currentKeys)

	if len(onlyInDB) != 0 {
		if err := h.m.DeleteByKeys(onlyInDB); err != nil {
			c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
				Success: false,
				Message: "Error when delete keys from DB",
			})
			return
		}
	}

	if len(onlyInRequest) != 0 {
		distributions := make([]*model.Distribution, 0, len(onlyInRequest))
		for _, key := range onlyInRequest {
			if key == "" {
				continue
			}
			distributions = append(distributions, &model.Distribution{
				Key:    key,
				Holder: req.Holder,
				Group:  req.Group,
			})
		}

		if err := h.m.CreateDistributions(distributions); err != nil {
			h.log.Error(err, "failed to create distributions", "holder", req.Holder, "count", len(distributions))
			c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
				Success: false,
				Message: "Error when create distribution in batch" + err.Error(),
			})
			return
		}
	}

	duration := time.Since(start).Seconds()
	h.log.Info("distributions created successfully",
		"holder", req.Holder,
		"duration_seconds", duration,
		"delete_from_db", len(onlyInDB),
		"add_to_db", len(onlyInRequest),
	)
	c.JSON(http.StatusCreated, model.ImageAdvertiseResponse{
		Success: true,
		Message: "Distribution created!",
	})
}

func diffSets(a, b []string) (onlyA, onlyB []string) {
	setA := make(map[string]struct{}, len(a))
	setB := make(map[string]struct{}, len(b))

	for _, v := range a {
		setA[v] = struct{}{}
	}
	for _, v := range b {
		setB[v] = struct{}{}
	}

	// A - B
	for v := range setA {
		if _, found := setB[v]; !found {
			onlyA = append(onlyA, v)
		}
	}

	// B - A
	for v := range setB {
		if _, found := setA[v]; !found {
			onlyB = append(onlyB, v)
		}
	}

	return
}
