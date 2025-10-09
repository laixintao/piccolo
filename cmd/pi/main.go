package main

import (
	"errors"
	"fmt"
	"time"

	"context"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/registry"
	"github.com/laixintao/piccolo/pkg/sd"
	"github.com/laixintao/piccolo/pkg/state"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type Arguments struct {
	RegistryAddr string `arg:"--registry-listen-addr,env:REGISTRY_ADDR,required" help:"address to serve image registry (for local containerd, you can use 127.0.0.1, as long as it can be connected for your containerd)"`
	PiAddr       string `arg:"--pi-listen-addr,env:PI_ADDR,required" help:"address to serve downloading for other pi agents, other agents will download images from this address"`
	MetricsAddr  string `arg:"--metrics-listen-addr,required,env:METRICS_ADDR" help:"address to serve metrics."`

	ContainerdSock              string        `arg:"--containerd-sock,env:CONTAINERD_SOCK" default:"/run/containerd/containerd.sock" help:"Endpoint of containerd service."`
	ContainerdNamespace         string        `arg:"--containerd-namespace,env:CONTAINERD_NAMESPACE" default:"k8s.io" help:"Containerd namespace to fetch images from."`
	ContainerdContentPath       string        `arg:"--containerd-content-path,env:CONTAINERD_CONTENT_PATH" default:"/var/lib/containerd/io.containerd.content.v1.content" help:"Path to Containerd content store"`
	Registries                  []url.URL     `arg:"--registries,env:REGISTRIES,required" help:"registries that are configured to be mirrored."`
	LogLevel                    slog.Level    `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	ResolveLatestTag            bool          `arg:"--resolve-latest-tag,env:RESOLVE_LATEST_TAG" default:"true" help:"When true latest tags will be resolved to digests."`
	PiccoloAddress              url.URL       `arg:"--piccolo-api,env:PICCOLO_ADDRESS" help:"Piccolo API URL for central service discovery"`
	FullRefreshMinutes          int64         `arg:"--full-refresh-minutes,env:PI_REFRESH_MINUTES" help:"pi will update all image states to piccolo for every X minutes."`
	MaxUploadConnections        int           `arg:"--max-upload-connections,env:MAX_UPLOAD_CONNECTIONS" default:"5" help:"Max connection used to upload images to other peers."`
	MaxUploadBlobBytesPerSecond float64       `arg:"--max-upload-blob-bytes-per-second,env:PI_MAX_UPLOAD_BLOB_BYTES_PER_SECOND" default:"1073741824" help:"Max upload speed limition for upload blobs to other pi nodes."`
	MirrorResolveTimeout        time.Duration `arg:"--mirror-resolve-timeout,env:MIRROR_RESOLVE_TIMEOUT" default:"20ms" help:"Max duration spent finding a mirror."`
	MirrorResolveRetries        int           `arg:"--mirror-resolve-retries,env:MIRROR_RESOLVE_RETRIES" default:"3" help:"Max amount of mirrors to attempt."`
	Group                       string        `arg:"--group,env:PI_GROUP,required" help:"The pi group name, pi can only discover other Pis in the same group."`
	Version                     bool          `arg:"-v,--version" help:"show version"`
}

func main() {
	fmt.Println("Hello, Pi!")

	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("Pi Version: %s\nCommit: %s\nBuilt: %s\n", version, commit, date)
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
	log.Info("log init")
	ctx := logr.NewContext(context.Background(), log)
	ociClient, err := oci.NewContainerd(ctx, args.ContainerdSock, args.ContainerdNamespace, args.Registries, oci.WithContentPath(args.ContainerdContentPath))
	if err != nil {
		log.Error(err, "run exit with error")
		os.Exit(1)
	}
	log.Info("containerd sdk init")

	piccoloSD, err := sd.NewPiccoloServiceDiscover(args.PiccoloAddress, log, args.PiAddr, args.Group)
	if err != nil {
		log.Error(err, "NewPiccoloServiceDiscover error")
		os.Exit(1)
	}

	g, ctx := errgroup.WithContext(ctx)

	err = startMetricsServer(ctx, args.MetricsAddr, g)
	if err != nil {
		log.Error(err, "Error when start Pi Server")
		os.Exit(1)
	}
	log.Info("Metrics server started", "address", args.PiAddr)

	// Pi Server
	err = startPiServer(ctx, args.Group, args.MaxUploadConnections, args.MaxUploadBlobBytesPerSecond, ociClient, piccoloSD, log, args.PiAddr, g)
	if err != nil {
		log.Error(err, "Error when start Pi Server")
		os.Exit(1)
	}
	log.Info("Start Pi server", "address", args.PiAddr)

	// Registry
	registryOpts := []registry.Option{
		registry.WithResolveLatestTag(args.ResolveLatestTag),
		registry.WithResolveRetries(args.MirrorResolveRetries),
		registry.WithResolveTimeout(args.MirrorResolveTimeout),
	}
	err = startRegistryServer(ctx, ociClient, piccoloSD, log, args.RegistryAddr, g, registryOpts...)
	if err != nil {
		log.Error(err, "Error when start Registry Server")
		os.Exit(1)
	}
	log.Info("Start registry server", "address", args.RegistryAddr)

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

func startPiServer(ctx context.Context, group string, maxConnection int,
	maxUploadBlobSpeedBytes float64,
	ociClient oci.Client, sd sd.ServiceDiscover, log logr.Logger, piAddr string, g *errgroup.Group) error {
	piServerOptions := []registry.PiServerOption{
		registry.WithMaxUploadConnection(maxConnection),
		registry.WithMaxUploadBlobSpeedBytes(maxUploadBlobSpeedBytes),
	}
	reg := registry.NewPiServer(ociClient, group, log, sd, piServerOptions...)
	regSrv, err := reg.Server(piAddr)
	if err != nil {
		return err
	}
	g.Go(func() error {
		if err := regSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return regSrv.Shutdown(shutdownCtx)
	})
	return nil
}

func startRegistryServer(ctx context.Context,
	ociClient oci.Client, sd sd.ServiceDiscover, log logr.Logger, registryAddress string, g *errgroup.Group, registryOpts ...registry.Option) error {
	reg := registry.NewRegistry(sd, log, registryOpts...)
	regSrv, err := reg.Server(registryAddress)
	if err != nil {
		return err
	}

	g.Go(func() error {
		if err := regSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return regSrv.Shutdown(shutdownCtx)
	})

	return nil
}

func startMetricsServer(ctx context.Context,
	metricsAddr string,
	g *errgroup.Group,
) error {
	metrics.Register()
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.DefaultGatherer, promhttp.HandlerOpts{}))
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	metricsSrv := &http.Server{
		Addr:    metricsAddr,
		Handler: mux,
	}
	g.Go(func() error {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return metricsSrv.Shutdown(shutdownCtx)
	})
	return nil
}
