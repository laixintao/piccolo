package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
)

type DistributionHandler struct {
	m   *storage.Manager
	log logr.Logger
}

func NewDistributionHandler(m *storage.Manager, log logr.Logger) *DistributionHandler {
	return &DistributionHandler{
		m:   m,
		log: log,
	}
}

// AdvertiseImage handles an advertise request.
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
	if err := validateHolder(req.Holder); err != nil {
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{Success: false, Message: err.Error()})
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

	if err := h.m.Distribution.CreateDistributions(distributions, req.Group); err != nil {
		h.log.Error(err, "failed to create distributions", "holder", req.Holder, "count", len(distributions))
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Failed to create distributions",
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

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

	holders, err := h.m.Distribution.GetHolderByKey(ctx, req.Group, req.Key)
	if err != nil {
		h.log.Error(err, "failed to get holders by key", "key", req.Key)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to find holders",
		})
		return
	}

	metrics.FindKeyHolderCountBucket.Observe(float64(len(holders)))

	if len(holders) == 0 {
		c.JSON(http.StatusNotFound,
			gin.H{"message": fmt.Sprintf("Didn't find the key %s in piccolo", req.Key)},
		)
		return
	}

	// Pick the closest holder by IP, shuffle the rest randomly
	sorted := holders
	start := time.Now()

	if req.RequestHost != "" {
		sorted, err = closestFirstThenShuffle(holders, req.RequestHost)
		if err != nil {
			c.JSON(http.StatusBadRequest,
				gin.H{"message": "error when sort holder's order", "err": err.Error()},
			)
			return
		}
	}
	sortDuration := time.Since(start).Seconds()

	h.log.Info("found holders for key", "group", req.Group, "key", req.Key, "queried_from_db", len(holders), "sort_cost_seconds", sortDuration)

	// Get limited holders if count is specified
	limit := 100
	if req.Count > 0 {
		limit = req.Count
	}
	if limit > len(sorted) {
		limit = len(sorted)
	}

	c.JSON(http.StatusOK, model.FindKeyResponse{
		Key:     req.Key,
		Holders: sorted[:limit],
		Group:   req.Group,
		Total:   len(holders),
	})
}

// Sync reconciles all keys stored for a holder.
// POST /api/v1/distribution/sync
func (h *DistributionHandler) Sync(c *gin.Context) {
	start := time.Now()
	var req model.ImageSyncRequest
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
	if err := validateHolder(req.Holder); err != nil {
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{Success: false, Message: err.Error()})
		return
	}

	existingKeys, err := h.m.Distribution.GetKeysByHolder(req.Group, req.Holder)
	if err != nil {
		h.log.Error(err, "failed to get keys by holder", "holder", req.Holder, "group", req.Group)
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when delete keys from DB",
		})
		return
	}

	currentKeys := *req.Keys

	onlyInDB, onlyInRequest := diffSets(existingKeys, currentKeys)

	if len(onlyInDB) != 0 {
		if err := h.m.Distribution.DeleteKeysByHolder(onlyInDB, req.Holder, req.Group); err != nil {
			h.log.Error(err, "failed to delete keys by holder", "holder", req.Holder, "group", req.Group)
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

		if err := h.m.Distribution.CreateDistributions(distributions, req.Group); err != nil {
			h.log.Error(err, "failed to create distributions", "holder", req.Holder, "count", len(distributions))
			c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
				Success: false,
				Message: "Failed to create distributions",
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
		Message: "Distribution synchronized!",
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

// lcpBits4 returns the number of leading equal bits between two IPv4 addrs.
// Both a and b must be IPv4.
func lcpBits4(a, b netip.Addr) int {
	ba := a.As4()
	bb := b.As4()

	lcp := 0
	for i := 0; i < 4; i++ {
		x := ba[i] ^ bb[i]
		if x == 0 {
			lcp += 8
			continue
		}
		// Count leading zeros in the first differing byte
		for bit := 7; bit >= 0; bit-- {
			if (x>>uint(bit))&1 == 0 {
				lcp++
			} else {
				return lcp
			}
		}
	}
	return lcp
}

// closestFirstThenShuffle finds the holder with the longest common prefix (most
// similar IPv4 address) and moves it to position 0. The rest remain as-is.
func closestFirstThenShuffle(hostports []string, target string) ([]string, error) {
	t, err := netip.ParseAddr(target)
	if err != nil {
		return nil, fmt.Errorf("parse target %q: %w", target, err)
	}
	t = t.Unmap()
	if !t.Is4() {
		return nil, fmt.Errorf("target %q is not IPv4", target)
	}

	if len(hostports) == 0 {
		return hostports, nil
	}

	bestIdx := 0
	bestLCP := -1
	for i, hp := range hostports {
		ap, err := netip.ParseAddrPort(hp)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", hp, err)
		}
		ip := ap.Addr().Unmap()
		if !ip.Is4() {
			return nil, fmt.Errorf("%q is not IPv4", hp)
		}
		lcp := lcpBits4(ip, t)
		if lcp > bestLCP {
			bestLCP = lcp
			bestIdx = i
		}
	}

	hostports[0], hostports[bestIdx] = hostports[bestIdx], hostports[0]
	return hostports, nil
}

func (h *DistributionHandler) KeepAlive(c *gin.Context) {
	var req model.KeepAliveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "keepalive failed to bind JSON request")
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Wrong request format: " + err.Error(),
		})
		return
	}
	if err := validateHolder(req.HostAddr); err != nil {
		c.JSON(http.StatusBadRequest, model.KeepAliveResponse{Success: false, Message: err.Error()})
		return
	}

	if err := h.m.Host.RefreshHostAddr(req.HostAddr, req.Group); err != nil {
		h.log.Error(err, "Failed to refresh host Addr!", "host_addr", req.HostAddr)
		c.JSON(http.StatusInternalServerError, model.KeepAliveResponse{
			Success: false,
			Message: "Failed to keepalive",
		})
		return
	}

	h.log.Info("Keepalive for host success", "host_addr", req.HostAddr)
	c.JSON(http.StatusCreated, model.KeepAliveResponse{
		Success: true,
		Message: "keep alive success",
	})

}

func validateHolder(holder string) error {
	addrPort, err := netip.ParseAddrPort(holder)
	if err != nil {
		return fmt.Errorf("holder must be an IP address and port: %w", err)
	}
	if !addrPort.Addr().Unmap().Is4() {
		return fmt.Errorf("holder must use IPv4")
	}
	return nil
}
