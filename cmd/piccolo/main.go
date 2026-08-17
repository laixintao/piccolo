package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

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

const maxRequestBodyBytes = 16 << 20

type GlobalArgs struct {
	LogLevel slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	Version  bool       `arg:"-v,--version" help:"show version"`
}

type ServerCmd struct {
	GlobalArgs
	PiccoloAddress string   `arg:"--piccolo-address,env:HOST" default:"0.0.0.0:7789" help:"Piccolo HTTP address"`
	EnableEvictor  bool     `arg:"--enable-evictor,env:ENABLE_EVICTOR" default:"false" help:"Enable evictor to clean up dead hosts automatically"`
	EnablePprof    bool     `arg:"--enable-pprof,env:ENABLE_PPROF" default:"false" help:"Expose Go pprof handlers on the HTTP listener"`
	DbDsnList      []string `arg:"--db-dsn-list,env:DB_DSN_LIST,required" help:"DB DSN list in format '<group>:<dbtype>:<dsn>'. dbtype can be 'master' or 'slave'. Example: 'default:master:user:pass@tcp(host:3306)/db1' 'us-1:master:user:pass@tcp(host:3306)/db2'"`
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

	// Preserve the pre-subcommand CLI for existing deployments: options at the
	// top level are treated as server options. Explicit help still shows the
	// command overview.
	if len(os.Args) > 1 && os.Args[1] != "server" && os.Args[1] != "migrate-db" && os.Args[1] != "--help" && os.Args[1] != "-h" {
		os.Args = append([]string{os.Args[0], "server"}, os.Args[1:]...)
	}

	args := &Arguments{}
	parser := arg.MustParse(args)

	// Default to server command if no subcommand specified
	if args.Server == nil && args.Migrate == nil {
		if len(os.Args) == 1 && os.Getenv("DB_DSN_LIST") == "" {
			parser.WriteHelp(os.Stdout)
			return
		}
		// Re-parse with server as default
		oldArgs := os.Args
		os.Args = append([]string{os.Args[0], "server"}, os.Args[1:]...)
		args = &Arguments{}
		arg.MustParse(args)
		os.Args = oldArgs
	}

	if args.Server != nil {
		if err := runServer(args.Server); err != nil {
			fmt.Fprintf(os.Stderr, "piccolo server failed: %v\n", err)
			os.Exit(1)
		}
	} else if args.Migrate != nil {
		runMigrate(args.Migrate)
	} else {
		parser.WriteHelp(os.Stdout)
		os.Exit(1)
	}
}

func runServer(args *ServerCmd) error {
	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init, Piccolo started")

	db, groups, masterResolvers, err := storage.InitMySQL(args.DbDsnList)
	if err != nil {
		return fmt.Errorf("connect to MySQL: %w", err)
	}

	log.Info("MySQL database connected", "groups", groups, "masterResolvers", masterResolvers)

	dbm := storage.NewManager(db, groups, masterResolvers)
	apiHandler := distributionHandler.NewDistributionHandler(dbm, log)
	defer func() {
		if err := dbm.Close(); err != nil {
			log.Error(err, "failed to close database")
		}
	}()

	log.Info("image store initialized")

	r := gin.Default()

	r.Use(func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
		}
		c.Next()
	})
	r.Use(middleware.HandlerMetricsMiddleware())

	registerVersionMetric()
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "ok",
		})
	})

	if args.EnablePprof {
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
	}

	v1 := r.Group("/api/v1")
	{
		v1.POST("/keepalive", apiHandler.KeepAlive)
		images := v1.Group("/distribution")
		{
			images.POST("/advertise", apiHandler.AdvertiseImage)
			images.GET("/findkey", apiHandler.FindKey)
			images.POST("/sync", apiHandler.Sync)
		}
	}

	log.Info("server starting", "piccolo-address", args.PiccoloAddress, "evictor-enabled", args.EnableEvictor, "pprof-enabled", args.EnablePprof)

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx := logr.NewContext(signalCtx, log)

	// Set evictor enabled metric
	if args.EnableEvictor {
		metrics.EvictorEnabled.Set(1)
		log.Info("Evictor enabled, starting background cleanup goroutine")
		go evictor.StartEvictor(ctx, dbm)
	} else {
		metrics.EvictorEnabled.Set(0)
		log.Info("Evictor disabled, dead hosts will not be cleaned up automatically")
	}

	srv := &http.Server{
		Addr:              args.PiccoloAddress,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
	}
	log.Info("Piccolo stopped")
	return nil
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
		log.Info("Migrating database", "index", i+1, "total", len(args.Databases))

		if err := migrateDatabase(dsn); err != nil {
			log.Error(err, "failed to migrate database schema", "index", i+1)
			os.Exit(1)
		}
		log.Info("Database schema migrated successfully", "index", i+1)
	}

	log.Info("All databases migration completed successfully!", "total", len(args.Databases))
}

func migrateDatabase(dsn string) error {
	// The migration command accepts a plain MySQL DSN and uses it as the
	// default master connection.
	db, _, _, err := storage.InitMySQL([]string{"default:master:" + dsn})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database connection: %w", err)
	}
	defer sqlDB.Close()

	return storage.AutoMigrate(db, &model.Distribution{}, &model.Host{})
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
