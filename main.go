package main

import (
	"fmt"

	"context"
	"net/url"
	"github.com/alexflint/go-arg"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/oci"
	"log/slog"
	"os"
)

type Arguments struct {
	ContainerdSock               string     `arg:"--containerd-sock,env:CONTAINERD_SOCK" default:"/run/containerd/containerd.sock" help:"Endpoint of containerd service."`
	ContainerdNamespace          string     `arg:"--containerd-namespace,env:CONTAINERD_NAMESPACE" default:"k8s.io" help:"Containerd namespace to fetch images from."`
	ContainerdContentPath        string     `arg:"--containerd-content-path,env:CONTAINERD_CONTENT_PATH" default:"/var/lib/containerd/io.containerd.content.v1.content" help:"Path to Containerd content store"`
	ContainerdRegistryConfigPath string     `arg:"--containerd-registry-config-path,env:CONTAINERD_REGISTRY_CONFIG_PATH" default:"/etc/containerd/certs.d" help:"Directory where mirror configuration is written."`
	Registries                   []url.URL  `arg:"--registries,env:REGISTRIES,required" help:"registries that are configured to be mirrored."`
	LogLevel                     slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
}

func main() {
	fmt.Println("Hello, Pi!")

	args := &Arguments{}
	arg.MustParse(args)

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewJSONHandler(os.Stderr, &opts)
	log := logr.FromSlogHandler(handler)
	ctx := logr.NewContext(context.Background(), log)
	ociClient, err := oci.NewContainerd(ctx, args.ContainerdSock, args.ContainerdNamespace, args.ContainerdRegistryConfigPath, args.Registries, oci.WithContentPath(args.ContainerdContentPath))
	if err != nil {
		log.Error(err, "run exit with error")
		os.Exit(1)
	}
	img, err := ociClient.ListImages(ctx)
	if err != nil {
		log.Error(err, "Get image error")
		os.Exit(1)
	}
	for _, item := range img {
		fmt.Printf("Image name: %s", item)
	}
}
