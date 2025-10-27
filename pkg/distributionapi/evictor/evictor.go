package evictor

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/randduration"
	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
)

const (
	EVICTORCHECKTIME = 10 * time.Minute
)

// If server not report health over 10 minutes, delete it
// from host_tab and distribution_tab
func StartEvictor(ctx context.Context, m *storage.Manager) error {
	log := logr.FromContextOrDiscard(ctx)

	// at least wait for 10 minutes, in case that:
	// api down
	// api started
	// then start to delete all the hosts...
	// so we wait at least a EVICTORCHECKTIME, to give all hosts one chance
	// to register themself
	sleepDuration := randduration.RandomDuration(EVICTORCHECKTIME)
	sleepDuration += EVICTORCHECKTIME
	log.Info("Healthcheck will be reset in", "sleepDuration", sleepDuration, "EVICTORCHECKTIME", EVICTORCHECKTIME)

	select {
	case <-time.After(sleepDuration):
		log.Info("Heart beat first trigger wait period over.")
	case <-ctx.Done():
		return nil
	}

	log.Info("First healthcheck starts, then trigger for every", "minutes", EVICTORCHECKTIME)
	if err := evictDeadHosts(ctx, m); err != nil {
		log.Error(err, "Error when do keepalive")
	}

	// update for const interval
	keepaliveTicker := time.NewTicker(time.Duration(EVICTORCHECKTIME))
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-keepaliveTicker.C:
			log.Info("By Ticker: Running Evictor...")
			if err := evictDeadHosts(ctx, m); err != nil {
				log.Error(err, "Error when do keepalive")
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func evictDeadHosts(ctx context.Context, m *storage.Manager) error {
	metrics.EvictorRunTotal.WithLabelValues().Inc()
	start := time.Now()
	defer func() {
		metrics.EvictorDuration.WithLabelValues().Observe(time.Since(start).Seconds())

	}()
	log := logr.FromContextOrDiscard(ctx)
	deadHosts, err := m.Host.FindDeadHosts()
	if err != nil {
		return err
	}

	for _, dh := range deadHosts {
		metrics.EvictorDeletedHostTotal.WithLabelValues().Inc()
		log.Info("Evict dead host", "host", dh)
		err := m.Distribution.DeleteByHolder(dh)
		if err != nil {
			log.Error(err, "Error when delete distributions by holder", "holder", dh)
			continue
		}
		err = m.Host.DeleteHost(dh)
		if err != nil {
			log.Error(err, "Error when delete Hosts by host_tab", "holder", dh)
			continue
		}
	}

	return nil
}
