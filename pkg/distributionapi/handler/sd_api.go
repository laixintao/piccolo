package handler

import (
	"context"
	"fmt"
	"math/rand/v2"
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
	m          *storage.Manager
	log        logr.Logger
	peers      *peerCache
	randomIntN func(int) int
}

func NewDistributionHandler(m *storage.Manager, log logr.Logger, config PeerCacheConfig) (*DistributionHandler, error) {
	peers, err := newPeerCache(config, m.Distribution.GetHolderWindow)
	if err != nil {
		return nil, err
	}
	return &DistributionHandler{
		m: m, log: log, peers: peers, randomIntN: rand.IntN,
	}, nil
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

	if err := h.m.Distribution.CreateDistributions(distributions, req.Group); err != nil {
		h.log.Error(err, "failed to create distributions", "holder", req.Holder, "count", len(distributions))
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when create distribution in batch" + err.Error(),
		})
		return
	}

	h.peers.invalidate(req.Group, req.Keys...)
	h.log.Info("distributions created successfully", "holder", req.Holder, "count", len(distributions))
	c.JSON(http.StatusCreated, model.ImageAdvertiseResponse{
		Success: true,
		Message: "Distribution created!",
	})
}

// FindKey finds holders for a key, excluding the request_host IP when supplied.
// GET /api/v1/distribution/findkey?key=xxx&count=10&group=xxx&request_host=10.0.0.1
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

	holders, cacheResult, err := h.peers.get(ctx, req.Group, req.Key)
	if err != nil {
		h.log.Error(err, "failed to get holders by key", "key", req.Key)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Error when finding holders: " + err.Error(),
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

	// The cache returns a private copy: filtering and shuffling must not change
	// the shared window or its pagination cursor.
	peers := holders
	start := time.Now()

	if req.RequestHost != "" {
		peers, err = excludeRequester(holders, req.RequestHost)
		if err != nil {
			c.JSON(http.StatusNotFound,
				gin.H{"message": "error when filtering holders", "err": err.Error()},
			)
			return
		}
	}
	if len(peers) == 0 {
		c.JSON(http.StatusNotFound,
			gin.H{"message": fmt.Sprintf("Didn't find another holder for key %s in piccolo", req.Key)},
		)
		return
	}

	// Get limited holders if count is specified
	limit := 100
	if req.Count > 0 {
		limit = req.Count
	}
	if limit > len(peers) {
		limit = len(peers)
	}
	// A partial Fisher-Yates shuffle samples without replacement and randomizes
	// the first attempt as well as the fallback peers. Do not re-sort by IP.
	for i := 0; i < limit; i++ {
		j := i + h.randomIntN(len(peers)-i)
		peers[i], peers[j] = peers[j], peers[i]
	}
	h.log.Info("found holders for key", "group", req.Group, "key", req.Key, "request_host", req.RequestHost,
		"candidate_count", len(holders), "excluded_self_count", len(holders)-len(peers),
		"cache_result", cacheResult, "selection", "random", "returned_count", limit,
		"selection_cost_seconds", time.Since(start).Seconds())

	c.JSON(http.StatusOK, model.FindKeyResponse{
		Key:     req.Key,
		Holders: peers[:limit],
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

	existingKeys, err := h.m.Distribution.GetKeysByHolder(req.Group, req.Holder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when delete keys from DB",
		})
		return
	}

	currentKeys := req.Keys

	onlyInDB, onlyInRequest := diffSets(existingKeys, currentKeys)

	if len(onlyInDB) != 0 {
		if err := h.m.Distribution.DeleteKeysByHolder(onlyInDB, req.Holder, req.Group); err != nil {
			c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
				Success: false,
				Message: "Error when delete keys from DB",
			})
			return
		}
		h.peers.invalidate(req.Group, onlyInDB...)
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
				Message: "Error when create distribution in batch" + err.Error(),
			})
			return
		}
		h.peers.invalidate(req.Group, onlyInRequest...)
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

// excludeRequester removes holders on the requester IP, across all ports.
// The input belongs to this request, so filtering can reuse its backing array.
func excludeRequester(hostports []string, target string) ([]string, error) {
	requester, err := netip.ParseAddr(target)
	if err != nil {
		return nil, fmt.Errorf("parse target %q: %w", target, err)
	}
	requester = requester.Unmap()
	if !requester.Is4() {
		return nil, fmt.Errorf("target %q is not IPv4", target)
	}
	peers := hostports[:0]
	for _, holder := range hostports {
		address, err := netip.ParseAddrPort(holder)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", holder, err)
		}
		ip := address.Addr().Unmap()
		if !ip.Is4() {
			return nil, fmt.Errorf("%q is not IPv4", holder)
		}
		if ip != requester {
			peers = append(peers, holder)
		}
	}
	return peers, nil
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
