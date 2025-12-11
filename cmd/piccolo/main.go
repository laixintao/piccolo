package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/distributionapi/evictor"
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

type GlobalArgs struct {
	LogLevel slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	Version  bool       `arg:"-v,--version" help:"show version"`
}

type ServerCmd struct {
	GlobalArgs
	PiccoloAddress string   `arg:"--piccolo-address,env:HOST" default:"0.0.0.0:7789" help:"Piccolo HTTP address"`
	EnableEvictor  bool     `arg:"--enable-evictor,env:ENABLE_EVICTOR" default:"false" help:"Enable evictor to clean up dead hosts automatically"`
	DbDsnList      []string `arg:"--db-dsn-list,env:DB_SLAVES_DSN" help:"DB DSN list, the format is "<group>:<dbtype>:<dsn>", 
	means that for this <group>("default" for all groups), piccolo
	will use <dsn>, <dbtype> is for mysql db is master or slave.
	(mysql master), "replica" for read only requests (mysql slave). 
	for exmaple, if you put: --db-dsn-list "default:master:username:password@tcp(127.0.0.1:3306)/db1"
	"default:slave:username:password@tcp(127.0.0.1:3306)/db2"
	"us-1:master:username:password@tcp(127.0.0.1:3306)/db3", then for us-1 group, all ready/write will goes to db3,
	all other read requests will go to db2, and all other write requests will go to db1.
	`
}

type MigrateCmd struct {
	GlobalArgs
	Databases []string `arg:"positional,required" help:"Database DSN(s) to migrate"`
}

type Arguments struct {
	Server  *ServerCmd  `arg:"subcommand:server" help:"Start Piccolo server"`
	Migrate *MigrateCmd `arg:"subcommand:migrate-db" help:"Migrate database schema to multiple databases"`
}

func (Arguments) Description() string {
	return "Piccolo - A distributed image distribution system"
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("Piccolo Version: %s\nCommit: %s\nBuilt: %s\n", version, commit, date)
			os.Exit(0)
		}
	}

	args := &Arguments{}
	parser := arg.MustParse(args)

	// Default to server command if no subcommand specified
	if args.Server == nil && args.Migrate == nil {
		// Re-parse with server as default
		oldArgs := os.Args
		os.Args = append([]string{os.Args[0], "server"}, os.Args[1:]...)
		args = &Arguments{}
		arg.MustParse(args)
		os.Args = oldArgs
	}

	if args.Server != nil {
		runServer(args.Server)
	} else if args.Migrate != nil {
		runMigrate(args.Migrate)
	} else {
		parser.WriteHelp(os.Stdout)
		os.Exit(1)
	}
}

func runServer(args *ServerCmd) {
	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init, Piccolo started")

	db, groups, masterResolvers, err := storage.InitMySQL(args.DbDsnList)
	if err != nil {
		log.Error(err, "failed to connect to MySQL database")
		os.Exit(1)
	}

	log.Info("MySQL database connected", "groups", groups, "masterResolvers", masterResolvers)

	dbm := storage.NewManager(db, groups, masterResolvers)
	distributionHandler := distributionHandler.NewDistributionHandler(dbm, log)
	defer dbm.Close()

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

	log.Info("server starting", "piccolo-address", args.PiccoloAddress, "evictor-enabled", args.EnableEvictor)

	ctx := logr.NewContext(context.Background(), log)
	
	// Set evictor enabled metric
	if args.EnableEvictor {
		metrics.EvictorEnabled.Set(1)
		log.Info("Evictor enabled, starting background cleanup goroutine")
		go evictor.StartEvictor(ctx, dbm)
	} else {
		metrics.EvictorEnabled.Set(0)
		log.Info("Evictor disabled, dead hosts will not be cleaned up automatically")
	}

	// Start server with configured host and port
	if err := r.Run(args.PiccoloAddress); err != nil {
		log.Error(err, "server failed to start")
		os.Exit(1)
	}
}

func runMigrate(args *MigrateCmd) {
	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("Piccolo database migration tool started", "total_databases", len(args.Databases))

	// Migrate each database
	for i, dsn := range args.Databases {
		log.Info("Migrating database", "index", i+1, "total", len(args.Databases), "dsn", dsn)

		// For migrate command, use simple single database connection (format: default:master:dsn_string)
		db, _, _, err := storage.InitMySQL([]string{"default:master:" + dsn})
		if err != nil {
			log.Error(err, "failed to connect to database", "index", i+1)
			os.Exit(1)
		}
		log.Info("Database connected", "index", i+1)

		if err := storage.AutoMigrate(db, &model.Distribution{}, &model.Host{}); err != nil {
			log.Error(err, "failed to migrate database schema", "index", i+1)
			os.Exit(1)
		}
		log.Info("Database schema migrated successfully", "index", i+1)
	}

	log.Info("All databases migration completed successfully!", "total", len(args.Databases))
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
