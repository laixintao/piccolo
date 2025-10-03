package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	imagehandler "github.com/laixintao/piccolo/internal/piccolo/handler"
	"github.com/laixintao/piccolo/internal/piccolo/storage"
)

type Arguments struct {
	Host     string     `arg:"--host,env:HOST" default:"0.0.0.0" help:"Host to listen on"`
	Port     int        `arg:"--port,env:PORT" default:"8080" help:"Port to listen on"`
	LogLevel slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	DBPath   string     `arg:"--db-path,env:DB_PATH" default:"./piccolo.db" help:"Path to the BoltDB database file"`
}

func main() {
	// Parse command line arguments
	args := &Arguments{}
	arg.MustParse(args)

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init, Piccolo started")

	// 确保数据库目录存在
	dbDir := filepath.Dir(args.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Error(err, "failed to create database directory")
		os.Exit(1)
	}

	// 初始化数据库存储
	imageStore, err := storage.NewBoltImageStore(args.DBPath)
	if err != nil {
		log.Error(err, "failed to initialize image store")
		os.Exit(1)
	}
	defer imageStore.Close()

	log.Info("database initialized", "path", args.DBPath)

	// 创建API处理器
	imageHandler := imagehandler.NewImageHandler(imageStore, log)

	// Create a Gin router with default middleware (logger and recovery)
	r := gin.Default()

	// 健康检查端点
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	// API路由组
	v1 := r.Group("/api/v1")
	{
		// 镜像相关API
		images := v1.Group("/images")
		{
			images.POST("", imageHandler.CreateImage)       // 创建镜像记录
			images.GET("", imageHandler.ListImages)         // 获取镜像列表
			images.GET("/:id", imageHandler.GetImage)       // 获取单个镜像
			images.PUT("/:id", imageHandler.UpdateImage)    // 更新镜像记录
			images.DELETE("/:id", imageHandler.DeleteImage) // 删除镜像记录
		}
	}

	log.Info("server starting", "host", args.Host, "port", args.Port)

	// Start server with configured host and port
	addr := fmt.Sprintf("%s:%d", args.Host, args.Port)
	if err := r.Run(addr); err != nil {
		log.Error(err, "server failed to start")
		os.Exit(1)
	}
}
