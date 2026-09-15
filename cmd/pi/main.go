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
	"github.com/laixintao/piccolo/internal/logging"
	"github.com/laixintao/piccolo/pkg/metrics"
	"github.com/laixintao/piccolo/pkg/oci"
	"github.com/laixintao/piccolo/pkg/registry"
	"github.com/laixintao/piccolo/pkg/sd"
	"github.com/laixintao/piccolo/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
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
	ContainerdNamespace         string        `arg:"--containerd-namespace,env:CONTAINERD_NAMESPACE" default:"k8s.io" help:"Comma-separated containerd namespaces to fetch images from, in tag lookup priority order."`
	ContainerdContentPath       string        `arg:"--containerd-content-path,env:CONTAINERD_CONTENT_PATH" default:"/var/lib/containerd/io.containerd.content.v1.content" help:"Path to Containerd content store"`
	Registries                  []url.URL     `arg:"--registries,env:REGISTRIES,required" help:"registries that are configured to be mirrored."`
	LogLevel                    slog.Level    `arg:"--log-level,env:LOG_LEVEL" default:"INFO" help:"Minimum log level to output. Value should be DEBUG, INFO, WARN, or ERROR."`
	ResolveLatestTag            bool          `arg:"--resolve-latest-tag,env:RESOLVE_LATEST_TAG" default:"true" help:"When true latest tags will be resolved to digests."`
	PiccoloAddress              url.URL       `arg:"--piccolo-api,env:PICCOLO_ADDRESS" help:"Piccolo API URL for central service discovery"`
	FullRefreshMinutes          int64         `arg:"--full-refresh-minutes,env:PI_REFRESH_MINUTES" default:"60" help:"Positive interval in minutes between full image state updates."`
	MaxUploadConnections        int           `arg:"--max-upload-connections,env:MAX_UPLOAD_CONNECTIONS" default:"5" help:"Max connection used to upload images to other peers."`
	MaxUploadBlobBytesPerSecond float64       `arg:"--max-upload-blob-bytes-per-second,env:PI_MAX_UPLOAD_BLOB_BYTES_PER_SECOND" default:"1073741824" help:"Max upload speed limition for upload blobs to other pi nodes."`
	MirrorResolveTimeout        time.Duration `arg:"--mirror-resolve-timeout,env:MIRROR_RESOLVE_TIMEOUT" default:"2s" help:"Max duration spent finding a mirror."`
	MirrorResolveRetries        int           `arg:"--mirror-resolve-retries,env:MIRROR_RESOLVE_RETRIES" default:"3" help:"Max amount of mirrors to attempt."`
	Group                       string        `arg:"--group,env:PI_GROUP,required" help:"The pi group name, pi can only discover other Pis in the same group."`
	Version                     bool          `arg:"-v,--version" help:"show version"`
}

func (a Arguments) validate() error {
	// Converting minutes to time.Duration must produce a positive ticker interval.
	const maxRefreshMinutes = int64(1<<63-1) / int64(time.Minute)
	if a.FullRefreshMinutes <= 0 || a.FullRefreshMinutes > maxRefreshMinutes {
		return fmt.Errorf("--full-refresh-minutes must be between 1 and %d", maxRefreshMinutes)
	}
	return nil
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("Pi Version: %s\nCommit: %s\nBuilt: %s\n", version, commit, date)
			os.Exit(0)
		}
	}

	args := &Arguments{}
	parser := arg.MustParse(args)
	if err := args.validate(); err != nil {
		parser.Fail(err.Error())
	}

	opts := slog.HandlerOptions{
		AddSource: true,
		Level:     args.LogLevel,
	}
	handler := slog.NewTextHandler(os.Stdout, &opts)
	log := logr.FromSlogHandler(handler).WithValues("service", "pi")
	uploadLog := log.WithValues("component", logging.Upload)
	downloadLog := log.WithValues("component", logging.Download)
	controlLog := log.WithValues("component", logging.Piccolo)
	controlLog.Info("pi starting", "event", "startup", "subsystem", "lifecycle", "version", version, "group", args.Group)
	ctx := logr.NewContext(context.Background(), log)
	ociClient, err := oci.NewContainerd(logr.NewContext(ctx, controlLog.WithValues("subsystem", "containerd")), args.ContainerdSock, args.ContainerdNamespace, args.Registries, oci.WithContentPath(args.ContainerdContentPath))
	if err != nil {
		controlLog.Error(err, "containerd client initialization failed", "event", "startup_failed", "subsystem", "containerd")
		os.Exit(1)
	}
	controlLog.V(4).Info("containerd client initialized", "event", "startup_ready", "subsystem", "containerd")

	piccoloSD, err := sd.NewPiccoloServiceDiscover(args.PiccoloAddress, log, args.PiAddr, args.Group)
	if err != nil {
		controlLog.Error(err, "Piccolo client initialization failed", "event", "startup_failed")
		os.Exit(1)
	}

	g, ctx := errgroup.WithContext(ctx)

	err = startMetricsServer(ctx, args.MetricsAddr, g)
	if err != nil {
		controlLog.Error(err, "metrics server failed to start", "event", "startup_failed", "subsystem", "metrics")
		os.Exit(1)
	}
	controlLog.Info("metrics server started", "event", "server_started", "subsystem", "metrics", "address", args.MetricsAddr)

	// Pi Server
	err = startPiServer(ctx, args.Group, args.MaxUploadConnections, args.MaxUploadBlobBytesPerSecond, ociClient, piccoloSD, log, args.PiAddr, g)
	if err != nil {
		uploadLog.Error(err, "peer upload server failed to start", "event", "startup_failed")
		os.Exit(1)
	}
	uploadLog.Info("peer upload server started", "event", "server_started", "address", args.PiAddr, "max_bytes_per_second", args.MaxUploadBlobBytesPerSecond)

	// Registry
	registryOpts := []registry.Option{
		registry.WithResolveLatestTag(args.ResolveLatestTag),
		registry.WithResolveRetries(args.MirrorResolveRetries),
		registry.WithResolveTimeout(args.MirrorResolveTimeout),
	}
	err = startRegistryServer(ctx, ociClient, piccoloSD, log, args.RegistryAddr, g, registryOpts...)
	if err != nil {
		downloadLog.Error(err, "containerd registry server failed to start", "event", "startup_failed")
		os.Exit(1)
	}
	downloadLog.Info("containerd registry server started", "event", "server_started", "address", args.RegistryAddr)

	// State tracking
	g.Go(func() error {
		return state.Track(ctx, ociClient, piccoloSD, args.FullRefreshMinutes, args.ResolveLatestTag)
	})

	err = g.Wait()
	if err != nil {
		controlLog.Error(err, "pi stopped with an error", "event", "shutdown_failed", "subsystem", "lifecycle")
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
			return fmt.Errorf("upload server: %w", err)
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
			return fmt.Errorf("download server: %w", err)
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

	versionMetric := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pi_version",
			Help: "Pi  server version info",
		},
		[]string{"version", "commit", "date"},
	)
	versionMetric.WithLabelValues(version, commit, date).Set(1)
	metrics.DefaultRegisterer.MustRegister(versionMetric)

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
		Addr:     metricsAddr,
		Handler:  mux,
		ErrorLog: slog.NewLogLogger(logr.ToSlogHandler(logr.FromContextOrDiscard(ctx).WithValues("component", logging.Piccolo, "subsystem", "metrics", "event", "server_error")), slog.LevelError),
	}
	g.Go(func() error {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("metrics server: %w", err)
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
