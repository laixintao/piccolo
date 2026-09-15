package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"

	semver "github.com/Masterminds/semver/v3"
	"github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/go-logr/logr"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	backupDir = "_backup"
)

var _ Client = &Containerd{}

type Containerd struct {
	contentPath  string
	client       *containerd.Client
	clientGetter func() (*containerd.Client, error)
	clientMu     sync.Mutex
	namespaces   []string
	listFilter   string
	eventFilters []string
}

type Option func(*Containerd)

func WithContentPath(path string) Option {
	return func(c *Containerd) {
		c.contentPath = path
	}
}

func NewContainerd(ctx context.Context, sock, namespace string, registries []url.URL, opts ...Option) (*Containerd, error) {
	namespaceList, err := parseNamespaces(namespace)
	if err != nil {
		return nil, err
	}
	listFilter, eventFilter := createFilters(registries)
	eventFilters := make([]string, 0, len(namespaceList))
	for _, ns := range namespaceList {
		// Subscribe ORs separate filters; namespace must be ANDed with the
		// image filters in each expression.
		eventFilters = append(eventFilters, fmt.Sprintf("namespace==%q,%s", ns, eventFilter))
	}
	log := logr.FromContextOrDiscard(ctx)
	log.Info("ContainerdClient Created.", "namespaces", namespaceList, "listFilter", listFilter, "eventFilters", eventFilters)

	c := &Containerd{
		clientGetter: func() (*containerd.Client, error) {
			return containerd.New(sock, containerd.WithDefaultNamespace(namespaceList[0]))
		},
		namespaces:   namespaceList,
		eventFilters: eventFilters,
		listFilter:   listFilter,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func (c *Containerd) Client() (*containerd.Client, error) {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	var err error
	if c.client == nil {
		c.client, err = c.clientGetter()
	}
	return c.client, err
}

func parseNamespaces(value string) ([]string, error) {
	var result []string
	for _, ns := range strings.Split(value, ",") {
		ns = strings.TrimSpace(ns)
		if err := identifiers.Validate(ns); err != nil {
			return nil, fmt.Errorf("invalid containerd namespace %q: %w", ns, err)
		}
		if !slices.Contains(result, ns) {
			result = append(result, ns)
		}
	}
	return result, nil
}

// lookupInNamespaces searches only the configured namespaces, in order. A
// missing object permits fallback; other errors must not be hidden as misses.
func lookupInNamespaces[T any](ctx context.Context, namespaceList []string, lookup func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	for _, ns := range namespaceList {
		value, err := lookup(namespaces.WithNamespace(ctx, ns))
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, errdefs.ErrNotFound) {
			return zero, fmt.Errorf("namespace %q: %w", ns, err)
		}
		lastErr = fmt.Errorf("namespace %q: %w", ns, err)
	}
	return zero, errors.Join(ErrNotFound, lastErr)
}

func (c *Containerd) Name() string {
	return "containerd"
}

func (c *Containerd) Verify(ctx context.Context) error {
	log := logr.FromContextOrDiscard(ctx)
	client, err := c.Client()
	if err != nil {
		return err
	}
	ok, err := client.IsServing(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("could not reach Containerd service")
	}
	srv := runtimeapi.NewRuntimeServiceClient(client.Conn())

	versionResp, err := srv.Version(ctx, &runtimeapi.VersionRequest{})
	if err != nil {
		return err
	}
	version, err := semver.NewVersion(versionResp.GetRuntimeVersion())
	if err != nil {
		return err
	}
	constraint, err := semver.NewConstraint(">1-0")
	if err != nil {
		return err
	}
	if constraint.Check(version) {
		log.Info("unable to verify status response", "runtime_version", version.String())
		return nil
	}

	return nil
}

func (c *Containerd) Subscribe(ctx context.Context) (<-chan ImageEvent, <-chan error, <-chan error, error) {
	log := logr.FromContextOrDiscard(ctx)
	imgCh := make(chan ImageEvent)
	errCh := make(chan error)
	client, err := c.Client()
	if err != nil {
		return nil, nil, nil, err
	}
	envelopeCh, cErrCh := client.EventService().Subscribe(ctx, c.eventFilters...)
	go func() {
		defer func() {
			close(imgCh)
			close(errCh)
		}()
		for {
			select {
			case <-ctx.Done():
				log.Info("ctx done, return from subscriber")
				return
			case envelope, ok := <-envelopeCh:
				if !ok {
					log.Info("envelopeCh closed")
					return
				}
				if !slices.Contains(c.namespaces, envelope.Namespace) {
					continue
				}
				event, err := c.imageEvent(ctx, client, envelope)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("namespace %q: %w", envelope.Namespace, err):
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case imgCh <- event:
				case <-ctx.Done():
					return
				}
			} // select
		} // for
	}() // goroutine
	return imgCh, errCh, cErrCh, nil
}

func (c *Containerd) imageEvent(ctx context.Context, client *containerd.Client, envelope *events.Envelope) (ImageEvent, error) {
	imageName, eventType, err := getEventImage(envelope.Event)
	if err != nil {
		return ImageEvent{}, err
	}
	event := ImageEvent{ImageName: imageName, Type: eventType, Namespace: envelope.Namespace}
	if eventType == DeleteEvent {
		// The image is already gone; state tracking will sync the union of all
		// configured namespaces so content held elsewhere stays advertised.
		logr.FromContextOrDiscard(ctx).Info("Delete image", "imageName", imageName, "namespace", envelope.Namespace)
		return event, nil
	}
	cImg, err := client.GetImage(namespaces.WithNamespace(ctx, envelope.Namespace), imageName)
	if err != nil {
		return ImageEvent{}, err
	}
	event.Image, err = Parse(cImg.Name(), cImg.Target().Digest)
	return event, err
}

func (c *Containerd) ListImages(ctx context.Context) ([]Image, error) {
	log := logr.FromContextOrDiscard(ctx)
	client, err := c.Client()
	if err != nil {
		return nil, err
	}
	log.Info("list images with filter", "filter", c.listFilter, "namespaces", c.namespaces)
	imgs := []Image{}
	seen := map[Image]struct{}{}
	for _, ns := range c.namespaces {
		cImgs, err := client.ListImages(namespaces.WithNamespace(ctx, ns), c.listFilter)
		if err != nil {
			return nil, fmt.Errorf("list images in namespace %q: %w", ns, err)
		}
		for _, cImg := range cImgs {
			if strings.HasPrefix(cImg.Name(), "sha256") {
				continue
			}
			img, err := Parse(cImg.Name(), cImg.Target().Digest)
			if err != nil {
				return nil, fmt.Errorf("parse image in namespace %q: %w", ns, err)
			}
			if _, ok := seen[img]; ok {
				continue
			}
			seen[img] = struct{}{}
			imgs = append(imgs, img)
		}
	}
	return imgs, nil
}

func (c *Containerd) Resolve(ctx context.Context, ref string) (digest.Digest, error) {
	client, err := c.Client()
	if err != nil {
		return "", err
	}
	return lookupInNamespaces(ctx, c.namespaces, func(ctx context.Context) (digest.Digest, error) {
		cImg, err := client.GetImage(ctx, ref)
		if err != nil {
			return "", err
		}
		return cImg.Target().Digest, nil
	})
}

func (c *Containerd) Size(ctx context.Context, dgst digest.Digest) (int64, error) {
	client, err := c.Client()
	if err != nil {
		return 0, err
	}
	return lookupInNamespaces(ctx, c.namespaces, func(ctx context.Context) (int64, error) {
		info, err := client.ContentStore().Info(ctx, dgst)
		return info.Size, err
	})
}

func (c *Containerd) GetManifest(ctx context.Context, dgst digest.Digest) ([]byte, string, error) {
	client, err := c.Client()
	if err != nil {
		return nil, "", err
	}
	b, err := lookupInNamespaces(ctx, c.namespaces, func(ctx context.Context) ([]byte, error) {
		return content.ReadBlob(ctx, client.ContentStore(), ocispec.Descriptor{Digest: dgst})
	})
	if err != nil {
		return nil, "", err
	}
	mt, err := DetermineMediaType(b)
	if err != nil {
		return nil, "", err
	}
	return b, mt, nil
}

func (c *Containerd) GetBlob(ctx context.Context, dgst digest.Digest) (io.ReadSeekCloser, error) {
	if c.contentPath != "" {
		// The physical blob directory is shared. Check namespace metadata before
		// reading it directly, just as the content-store API does.
		if _, err := c.Size(ctx, dgst); err != nil {
			return nil, err
		}
		path := filepath.Join(c.contentPath, "blobs", dgst.Algorithm().String(), dgst.Encoded())
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.Join(ErrNotFound, err)
		}
		if err != nil {
			return nil, err
		}
		return file, nil
	}
	client, err := c.Client()
	if err != nil {
		return nil, err
	}
	ra, err := lookupInNamespaces(ctx, c.namespaces, func(ctx context.Context) (content.ReaderAt, error) {
		return client.ContentStore().ReaderAt(ctx, ocispec.Descriptor{Digest: dgst})
	})
	if err != nil {
		return nil, err
	}
	return struct {
		io.ReadSeeker
		io.Closer
	}{
		ReadSeeker: io.NewSectionReader(ra, 0, ra.Size()),
		Closer:     ra,
	}, nil
}

func getEventImage(e typeurl.Any) (string, EventType, error) {
	if e == nil {
		return "", "", errors.New("any cannot be nil")
	}
	evt, err := typeurl.UnmarshalAny(e)
	if err != nil {
		return "", "", fmt.Errorf("failed to unmarshal any: %w", err)
	}
	switch e := evt.(type) {
	case *eventtypes.ImageCreate:
		return e.Name, CreateEvent, nil
	case *eventtypes.ImageUpdate:
		return e.Name, UpdateEvent, nil
	case *eventtypes.ImageDelete:
		return e.Name, DeleteEvent, nil
	default:
		return "", "", errors.New("unsupported event type")
	}
}

func createFilters(registries []url.URL) (string, string) {
	registryHosts := []string{}
	for _, registry := range registries {
		registryHosts = append(registryHosts, strings.ReplaceAll(registry.Host, `.`, `\\.`))
	}
	listFilter := fmt.Sprintf(`name~="^(%s)/"`, strings.Join(registryHosts, "|"))
	eventFilter := fmt.Sprintf(`topic~="/images/create|/images/update|/images/delete",event.%s`, listFilter)
	return listFilter, eventFilter
}

func validateRegistries(urls []url.URL) error {
	errs := []error{}
	for _, u := range urls {
		if u.Scheme != "http" && u.Scheme != "https" {
			errs = append(errs, fmt.Errorf("invalid registry url scheme must be http or https: %s", u.String()))
		}
		if u.Path != "" {
			errs = append(errs, fmt.Errorf("invalid registry url path has to be empty: %s", u.String()))
		}
		if len(u.Query()) != 0 {
			errs = append(errs, fmt.Errorf("invalid registry url query has to be empty: %s", u.String()))
		}
		if u.User != nil {
			errs = append(errs, fmt.Errorf("invalid registry url user has to be empty: %s", u.String()))
		}
	}
	return errors.Join(errs...)
}

func templateHosts(registryURL url.URL, mirrorURLs []url.URL, capabilities []string) (string, error) {
	server := registryURL.String()
	if registryURL.String() == "https://docker.io" {
		server = "https://registry-1.docker.io"
	}
	capabilitiesStr := strings.Join(capabilities, "', '")
	capabilitiesStr = fmt.Sprintf("['%s']", capabilitiesStr)
	hc := struct {
		Server       string
		Capabilities string
		MirrorURLs   []url.URL
	}{
		Server:       server,
		Capabilities: capabilitiesStr,
		MirrorURLs:   mirrorURLs,
	}
	tmpl, err := template.New("").Parse(`server = '{{ .Server }}'
{{ range .MirrorURLs }}
[host.'{{ .String }}']
capabilities = {{ $.Capabilities }}
{{ end }}`)
	if err != nil {
		return "", err
	}
	buf := bytes.NewBuffer(nil)
	err = tmpl.Execute(buf, hc)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

type hostFile struct {
	Hosts map[string]interface{} `toml:"host"`
}
