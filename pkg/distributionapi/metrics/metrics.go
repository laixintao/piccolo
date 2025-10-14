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
	DBInsertTotal= prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "piccolo_db_distribution_insert_total",
		Help: "Total number of mirror requests.",
	}, []string{})
)

func Register() {
	DefaultRegisterer.MustRegister(DBInsertTotal)
}
