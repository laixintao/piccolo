package evictor

import (
	"context"
	"math/rand"
	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
)

const (
	EVICTORCHECKTIME = 10 * time.Minute
)

// If server not report health over 10 minutes, delete it
// from host_tab and distribution_tab
func StartEvictor(ctx context.Context, dm *storage.DistributionManager) error {
	log := logr.FromContextOrDiscard(ctx)
	resetInMinutes := rand.Int63n(int64(EVICTORCHECKTIME))
	log.Info("Healthcheck will be reset in", "minutes", resetInMinutes, "EVICTORCHECKTIME", EVICTORCHECKTIME)

	select {
	case <-time.After(time.Duration(resetInMinutes) * time.Minute):
		log.Info("Heart beat first trigger wait period over.")
	case <-ctx.Done():
		return nil
	}

	log.Info("First healthcheck starts, then trigger for every", "minutes", EVICTORCHECKTIME)
	if err := evictDeadHosts(ctx, dm); err != nil {
		log.Error(err, "Error when do keepalive")
	}

	// update for const interval
	keepaliveTicker := time.NewTicker(time.Duration(EVICTORCHECKTIME))
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-keepaliveTicker.C:
			log.Info("By Ticker: Running Evictor...")
			if err := evictDeadHosts(ctx, dm); err != nil {
				log.Error(err, "Error when do keepalive")
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func evictDeadHosts(ctx context.Context, m *storage.DistributionManager) error {
	metrics.EvictorTotal.WithLabelValues().Inc()
	start := time.Now()
	defer func() {
		metrics.EvictorDuration.WithLabelValues().Observe(time.Since(start).Seconds())

	}()
	log := logr.FromContextOrDiscard(ctx)
	deadHosts, err := m.FindDeadHosts()
	if err != nil {
		return err
	}

	for _, dh := range deadHosts {
		log.Info("Evict dead host", "host", dh)
		err := m.DeleteByHolder(dh)
		if err != nil {
			log.Error(err, "Error when delete distributions by holder", "holder", dh)
			continue
		}
		err = m.DeleteHost(dh)
		if err != nil {
			log.Error(err, "Error when delete Hosts by host_tab", "holder", dh)
			continue
		}
	}

	return nil
}
