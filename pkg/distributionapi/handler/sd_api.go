package handler

import (
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
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
	limit := 100
	if req.Count > 0 {
		limit = req.Count
	}
	holders, err := h.m.GetHolderByKey(req.Group, req.Key)
	if err != nil {
		h.log.Error(err, "failed to get holders by key with limit", "key", req.Key, "count", req.Count)
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

	// sort by IP closing to the holder
	sorted := holders
	start := time.Now()
	sortDuration := time.Since(start).Seconds()

	if req.RequestHost != "" {
		sorted, err = sortByLCPv4HostPort(holders, req.RequestHost)
		if err != nil {
			c.JSON(http.StatusNotFound,
				gin.H{"message": "error when sort holder's order", "err": err.Error()},
			)
			return
		}
	}

	h.log.Info("found holders for key", "group", req.Group, "key", req.Key, "queryed_from_db", len(holders), "sort_cost_seconds", sortDuration)

	if limit > len(sorted) {
		limit = len(sorted)
	}

	c.JSON(http.StatusOK, model.FindKeyResponse{
		Key:     req.Key,
		Holders: sorted[:limit],
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

	existingKeys, err := h.m.GetKeysByHolder(req.Group, req.Holder)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Error when delete keys from DB",
		})
	}

	currentKeys := req.Keys

	onlyInDB, onlyInRequest := diffSets(existingKeys, currentKeys)

	if len(onlyInDB) != 0 {
		if err := h.m.DeleteByKeysByHolder(onlyInDB, req.Holder, req.Group); err != nil {
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

// sortByLCPv4HostPort sorts "ip:port" strings by the longest common prefix (bits)
// of their IPv4 address with the given target IPv4 address.
// Ports are ignored for ranking, but returned strings keep the original "ip:port" form.
func sortByLCPv4HostPort(hostports []string, target string) ([]string, error) {
	// Parse and validate target as IPv4
	t, err := netip.ParseAddr(target)
	if err != nil {
		return nil, fmt.Errorf("parse target %q: %w", target, err)
	}
	t = t.Unmap()
	if !t.Is4() {
		return nil, fmt.Errorf("target %q is not IPv4", target)
	}

	// Parse inputs and precompute LCP
	type item struct {
		hostport string // original "ip:port"
		ip       netip.Addr
		lcp      int
	}

	items := make([]item, 0, len(hostports))
	for _, hp := range hostports {
		ap, err := netip.ParseAddrPort(hp)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", hp, err)
		}
		ip := ap.Addr().Unmap()
		if !ip.Is4() {
			return nil, fmt.Errorf("%q is not IPv4", hp)
		}
		items = append(items, item{
			hostport: hp,
			ip:       ip,
			lcp:      lcpBits4(ip, t),
		})
	}

	// Sort by LCP desc; tie-breaker by numeric IP, then by port string for stability
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].lcp != items[j].lcp {
			return items[i].lcp > items[j].lcp
		}
		if items[i].ip != items[j].ip {
			return items[i].ip.Less(items[j].ip)
		}
		// Optional: tie-break by port lexicographically; preserves deterministic order
		return items[i].hostport < items[j].hostport
	})

	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].hostport
	}
	return out, nil
}

func (h *DistributionHandler) KeepAlive(c *gin.Context) {
	var req model.KeepAliveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "keepalive failed to bind JSON request")
		c.JSON(http.StatusBadRequest, model.ImageAdvertiseResponse{
			Success: false,
			Message: "Wrong request format: " + err.Error(),
		})
	}

	if err := h.m.RefreshHostAddr(req.HostAddr, req.Group); err != nil {
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
