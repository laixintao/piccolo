package main

import (
	"fmt"
	"net/http"
	"os"

	"log/slog"

	"github.com/alexflint/go-arg"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
)

type Arguments struct {
	Host     string     `arg:"--host,env:HOST" default:"0.0.0.0" help:"Host to listen on"`
	Port     int        `arg:"--port,env:PORT" default:"8080" help:"Port to listen on"`
	LogLevel slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
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

	// Create a Gin router with default middleware (logger and recovery)
	r := gin.Default()

	// Define a simple GET endpoint
	r.GET("/ping", func(c *gin.Context) {
		// Return JSON response
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	// Start server with configured host and port
	addr := fmt.Sprintf("%s:%d", args.Host, args.Port)
	r.Run(addr)
}
