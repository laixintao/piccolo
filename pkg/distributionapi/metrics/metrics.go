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
	}, []string{"operation"})
	
	DBQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "piccolo_db_query_duration_seconds",
		Help: "Duration of database queries in seconds.",
	}, []string{"operation"})
)

func Register() {
	DefaultRegisterer.MustRegister(DBQueryTotal)
	DefaultRegisterer.MustRegister(DBQueryDuration)
}
