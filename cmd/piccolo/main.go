package main

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	distributionHandler "github.com/laixintao/piccolo/pkg/distributionapi/handler"
	"github.com/laixintao/piccolo/pkg/distributionapi/metrics"
	"github.com/laixintao/piccolo/pkg/distributionapi/middleware"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type Arguments struct {
	PiccoloAddress string     `arg:"--piccolo-address,env:HOST" default:"0.0.0.0:7789" help:"Piccolo HTTP address"`
	LogLevel       slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	Version        bool       `arg:"-v,--version" help:"show version"`
	MigrateDB      bool       `arg:"--migrate-db" help:"Auto change database's schema"`
	DBMasterDSN    string     `arg:"--db-master-dsn,env:DB_MASTER_DSN,required" help:"Master db"`
	DBSlavesDSN    []string   `arg:"--db-slaves-dsn,env:DB_SLAVES_DSN" help:"Slave dbs, can be multiple, if set, read requests will only be sent to slave db"`
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("Piccolo Version: %s\nCommit: %s\nBuilt: %s\n", version, commit, date)
			os.Exit(0)
		}
	}
	args := &Arguments{}
	arg.MustParse(args)

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init, Piccolo started")

	db, err := storage.InitMySQL(args.DBMasterDSN, args.DBSlavesDSN)
	if err != nil {
		log.Error(err, "failed to connect to MySQL database")
		os.Exit(1)
	}

	log.Info("MySQL database connected")

	if args.MigrateDB {
		log.Info("Now apply datbase migration (DDL)...")
		if err := storage.AutoMigrate(db, &model.Distribution{}, &model.Host{}); err != nil {
			log.Error(err, "failed to run database migration")
			os.Exit(1)
		}
	}

	log.Info("database migration completed successfully")

	distributionManager := storage.NewDistributionManager(db)
	distributionHandler := distributionHandler.NewDistributionHandler(distributionManager, log)
	defer distributionManager.Close()

	log.Info("image store initialized")

	r := gin.Default()

	r.Use(middleware.HandlerMetricsMiddleware())

	registerVersionMetric()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "ok",
		})
	})

	// Register pprof endpoints
	pprofGroup := r.Group("/debug/pprof")
	{
		pprofGroup.GET("/", gin.WrapF(pprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
		pprofGroup.POST("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
		pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}
	log.Info("pprof endpoints registered at /debug/pprof")

	v1 := r.Group("/api/v1")
	{
		v1.POST("/keepalive", distributionHandler.KeepAlive)
		images := v1.Group("/distribution")
		{
			images.POST("/advertise", distributionHandler.AdvertiseImage)
			images.GET("/findkey", distributionHandler.FindKey)
			images.POST("/sync", distributionHandler.Sync)
		}
	}

	log.Info("server starting", "piccolo-address", args.PiccoloAddress)

	// Start server with configured host and port
	if err := r.Run(args.PiccoloAddress); err != nil {
		log.Error(err, "server failed to start")
		os.Exit(1)
	}
}

func registerVersionMetric() {
	versionMetric := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "piccolo_api_version",
			Help: "Piccolo server version info",
		},
		[]string{"version", "commit", "date"},
	)

	versionMetric.WithLabelValues(version, commit, date).Set(1)
	prometheus.MustRegister(versionMetric)
	metrics.Register()
}
