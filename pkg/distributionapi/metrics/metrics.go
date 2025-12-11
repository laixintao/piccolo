package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// DefaultRegisterer and DefaultGatherer are the implementations of the
	// prometheus Registerer and Gatherer interfaces that all metrics operations
	// will use. They are variables so that packages that embed this library can
	// replace them at runtime, instead of having to pass around specific
	// registries.
	DefaultRegisterer = prometheus.DefaultRegisterer
	DefaultGatherer   = prometheus.DefaultGatherer
)

var (
	DBQueryTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "piccolo_db_query_total",
		Help: "Total number of database queries.",
	}, []string{"table", "operation", "group"})

	DBQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "piccolo_db_query_duration_seconds",
		Help: "Duration of database queries in seconds.",
	}, []string{"table", "operation", "group"})

	FindKeyHolderCountBucket = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "piccolo_api_findkey_db_result_count",
			Help:    "Findkey get how many results from db",
			Buckets: []float64{10, 100, 200, 300, 500, 1000, 2000, 3000, 5000, 100000},
		},
	)

	EvictorRunTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "piccolo_evictor_run_total",
		Help: "Total number of evictor has been triggered",
	}, []string{})
	EvictorDeletedHostTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "piccolo_evictor_deleted_host_total",
		Help: "Dead hosts total, being deleted",
	}, []string{})

	EvictorDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "piccolo_evictor_duration_seconds",
		Help: "Duration of evictor run",
	}, []string{})

	EvictorEnabled = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "piccolo_evictor_enabled",
		Help: "Indicates whether evictor is enabled (1) or disabled (0)",
	})
)

func Register() {
	DefaultRegisterer.MustRegister(DBQueryTotal)
	DefaultRegisterer.MustRegister(DBQueryDuration)
	DefaultRegisterer.MustRegister(FindKeyHolderCountBucket)
	DefaultRegisterer.MustRegister(EvictorRunTotal)
	DefaultRegisterer.MustRegister(EvictorDuration)
	DefaultRegisterer.MustRegister(EvictorDeletedHostTotal)
	DefaultRegisterer.MustRegister(EvictorEnabled)
}
