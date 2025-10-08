package middleware

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"handler", "method", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"handler", "method"},
	)
)

func HandlerMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		status := c.Writer.Status()

		handler := c.HandlerName()
		if handler != "" {
			parts := strings.Split(handler, ".")
			handler = parts[len(parts)-1]
		}

		httpRequestsTotal.WithLabelValues(handler, c.Request.Method, fmt.Sprintf("%d", status)).Inc()
		httpRequestDuration.WithLabelValues(handler, c.Request.Method).Observe(duration)
	}
}
