package main

import (
	"fmt"

	"context"
	"github.com/alexflint/go-arg"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/sd"
	"github.com/laixintao/piccolo/pkg/state"
	"golang.org/x/sync/errgroup"
	"log/slog"
	"net/url"
	"os"
)

type Arguments struct {
	ContainerdSock        string     `arg:"--containerd-sock,env:CONTAINERD_SOCK" default:"/run/containerd/containerd.sock" help:"Endpoint of containerd service."`
	ContainerdNamespace   string     `arg:"--containerd-namespace,env:CONTAINERD_NAMESPACE" default:"k8s.io" help:"Containerd namespace to fetch images from."`
	ContainerdContentPath string     `arg:"--containerd-content-path,env:CONTAINERD_CONTENT_PATH" default:"/var/lib/containerd/io.containerd.content.v1.content" help:"Path to Containerd content store"`
	Registries            []url.URL  `arg:"--registries,env:REGISTRIES,required" help:"registries that are configured to be mirrored."`
	LogLevel              slog.Level `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	ResolveLatestTag      bool       `arg:"--resolve-latest-tag,env:RESOLVE_LATEST_TAG" default:"true" help:"When true latest tags will be resolved to digests."`
	PiccoloAddress        url.URL    `arg:"--piccolo-address,env:PICCOLO_ADDRESS" help:"Piccolo API URL for central service discovery"`
	FullRefreshMinutes    int64      `arg:"--full-refresh-minutes,env:PI_REFRESH_MINUTES" help:"pi will update all image states to piccolo for every X minutes."`
}

func main() {
	fmt.Println("Hello, Pi!")

	args := &Arguments{}
	arg.MustParse(args)

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler)
	log.Info("log init")
	ctx := logr.NewContext(context.Background(), log)
	ociClient, err := oci.NewContainerd(ctx, args.ContainerdSock, args.ContainerdNamespace, args.Registries, oci.WithContentPath(args.ContainerdContentPath))
	if err != nil {
		log.Error(err, "run exit with error")
		os.Exit(1)
	}
	log.Info("containerd sdk init")
	img, err := ociClient.ListImages(ctx)
	if err != nil {
		log.Error(err, "Get image error")
		os.Exit(1)
	}
	log.Info("list image")
	log.Info("Image", "image", img)

	for _, item := range img {
		log.Info("Get image details", "Name", item.Name,
			"Registry", item.Registry,
			"Repository", item.Repository,
			"Tag", item.Tag,
			"Digest", item.Digest,
		)
	}

	g, ctx := errgroup.WithContext(ctx)

	piccoloSD, err := sd.NewPiccoloServiceDiscover(args.PiccoloAddress)
	if err != nil {
		log.Error(err, "NewPiccoloServiceDiscover error")
		os.Exit(1)
	}

	// State tracking
	g.Go(func() error {
		return state.Track(ctx, ociClient, piccoloSD, args.FullRefreshMinutes, args.ResolveLatestTag)
	})

	err = g.Wait()
	if err != nil {
		log.Error(err, "Error when g.Wait()")
		os.Exit(1)
	}
}
