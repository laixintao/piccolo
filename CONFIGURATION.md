# Containerd namespaces

Pi defaults to the `k8s.io` namespace. To discover and serve images from both
Kubernetes and workloads using `default`, set:

```sh
--containerd-namespace=k8s.io,default
```

The same setting is available through the environment:

```sh
CONTAINERD_NAMESPACE=k8s.io,default
```

The existing single-namespace argument and environment variable remain valid.
Whitespace around names is ignored, duplicates are removed, and empty or invalid
namespace names are rejected at startup. Namespaces must be listed explicitly.

- Image events are filtered to the configured namespaces and read in the
  namespace that emitted the event. Event and lookup error logs include the
  namespace.
- Full sync advertises the union of images from all configured namespaces.
  Deleting an image in one namespace does not remove advertisements for content
  still referenced by an image in another configured namespace.
- Manifest and blob lookups by digest search the configured namespaces in order.
  This also applies when blobs are read directly from `--containerd-content-path`:
  sharing a physical content directory alone does not add another namespace.
- If the same tag points to different digests in different namespaces, the first
  configured namespace containing the tag wins when resolving it. Both images'
  digest keys remain available for P2P downloads.
- `--resolve-latest-tag=false` skips advertising and resolving the `latest` tag.
  Its manifest and blob digest keys are still advertised when image tracking and
  traversal succeed.
