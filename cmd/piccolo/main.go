package main

import (
	"net/http"
	"os"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	imagehandler "github.com/laixintao/piccolo/internal/piccolo/handler"
	"github.com/laixintao/piccolo/internal/piccolo/storage"
)

type Arguments struct {
	PiccoloAddress       string     `arg:"--piccolo-address,env:HOST" default:"0.0.0.0:7789" help:"Piccolo HTTP address"`
	LogLevel   slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	DBHost     string     `arg:"--db-host,env:DB_HOST" default:"localhost" help:"MySQL database host"`
	DBPort     int        `arg:"--db-port,env:DB_PORT" default:"3306" help:"MySQL database port"`
	DBUser     string     `arg:"--db-user,env:DB_USER" default:"root" help:"MySQL database user"`
	DBPassword string     `arg:"--db-password,env:DB_PASSWORD" default:"" help:"MySQL database password"`
	DBName     string     `arg:"--db-name,env:DB_NAME" default:"piccolo" help:"MySQL database name"`
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

	// 配置数据库连接
	dbConfig := &storage.DatabaseConfig{
		Host:     args.DBHost,
		Port:     args.DBPort,
		User:     args.DBUser,
		Password: args.DBPassword,
		Database: args.DBName,
		Charset:  "utf8mb4",
		ParseTime: true,
		Loc:      "Local",
		MaxIdleConns: 10,
		MaxOpenConns: 100,
	}

	// 初始化MySQL连接
	db, err := storage.InitMySQL(dbConfig)
	if err != nil {
		log.Error(err, "failed to connect to MySQL database")
		os.Exit(1)
	}

	log.Info("MySQL database connected", "host", args.DBHost, "database", args.DBName)

	// 初始化镜像存储
	imageStore, err := storage.NewGormImageStore(db)
	if err != nil {
		log.Error(err, "failed to initialize image store")
		os.Exit(1)
	}
	defer imageStore.Close()

	log.Info("image store initialized")

	imageHandler := imagehandler.NewImageHandler(imageStore, log)

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	v1 := r.Group("/api/v1")
	{
		images := v1.Group("/images")
		{
			images.POST("", imageHandler.CreateImage)          // 创建镜像记录
			images.GET("", imageHandler.ListImages)            // 获取镜像列表
			images.GET("/:uuid", imageHandler.GetImage)        // 获取单个镜像
			images.PUT("/:uuid", imageHandler.UpdateImage)     // 更新镜像记录
			images.DELETE("/:uuid", imageHandler.DeleteImage)  // 删除镜像记录
		}
	}

	log.Info("server starting", "piccolo-address", args.PiccoloAddress)

	// Start server with configured host and port
	if err := r.Run(args.PiccoloAddress); err != nil {
		log.Error(err, "server failed to start")
		os.Exit(1)
	}
}
