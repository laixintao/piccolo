package main

import (
	"net/http"
	"os"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	distributionHandler "github.com/laixintao/piccolo/pkg/distributionapi/handler"
	"github.com/laixintao/piccolo/pkg/distributionapi/middleware"
	"github.com/laixintao/piccolo/pkg/distributionapi/model"
	"github.com/laixintao/piccolo/pkg/distributionapi/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Arguments struct {
	PiccoloAddress string     `arg:"--piccolo-address,env:HOST" default:"0.0.0.0:7789" help:"Piccolo HTTP address"`
	LogLevel       slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	DBHost         string     `arg:"--db-host,env:DB_HOST" default:"localhost" help:"MySQL database host"`
	DBPort         int        `arg:"--db-port,env:DB_PORT" default:"3306" help:"MySQL database port"`
	DBUser         string     `arg:"--db-user,env:DB_USER" default:"root" help:"MySQL database user"`
	DBPassword     string     `arg:"--db-password,env:DB_PASSWORD" default:"" help:"MySQL database password"`
	DBName         string     `arg:"--db-name,env:DB_NAME" default:"piccolo" help:"MySQL database name"`
}

func main() {
	args := &Arguments{}
	arg.MustParse(args)

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init, Piccolo started")

	dbConfig := &storage.DatabaseConfig{
		Host:         args.DBHost,
		Port:         args.DBPort,
		User:         args.DBUser,
		Password:     args.DBPassword,
		Database:     args.DBName,
		Charset:      "utf8mb4",
		ParseTime:    true,
		Loc:          "Local",
		MaxIdleConns: 10,
		MaxOpenConns: 100,
	}

	db, err := storage.InitMySQL(dbConfig)
	if err != nil {
		log.Error(err, "failed to connect to MySQL database")
		os.Exit(1)
	}

	log.Info("MySQL database connected", "host", args.DBHost, "database", args.DBName)

	if err := storage.AutoMigrate(db, &model.Distribution{}); err != nil {
		log.Error(err, "failed to run database migration")
		os.Exit(1)
	}

	log.Info("database migration completed successfully")

	distributionManager := storage.NewDistributionManager(db)
	distributionHandler := distributionHandler.NewDistributionHandler(distributionManager, log)
	defer distributionManager.Close()

	log.Info("image store initialized")

	r := gin.Default()

	r.Use(middleware.HandlerMetricsMiddleware())
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))


	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "ok",
		})
	})

	v1 := r.Group("/api/v1")
	{
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
