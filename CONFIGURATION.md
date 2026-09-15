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

# Logging

Pi uses three `component` values to identify the work being done:

| Component | Work | Main events and fields |
| --- | --- | --- |
| `upload` | A peer requests content from this Pi | `request_finished`, `peer`, `client_ip`, `digest`, `bytes_sent`, `status`, `latency` |
| `download` | Containerd requests content; this Pi discovers and downloads from a peer | `peer_attempt`, `peer_failed`, `peer_download_finished`, `request_finished`, `key`, `peer`, `result` |
| `piccolo` | This Pi queries Piccolo, advertises keys, syncs state, and sends keepalives | `api_request_finished`, `operation`, `server`, `group`, `key_count` or `peer_count`, `result`, `latency` |

`component=piccolo subsystem=state` identifies local image tracking and sync
preparation. Containerd setup, process lifecycle, and metrics server messages
also use `component=piccolo`, with `subsystem=containerd`, `lifecycle`, or
`metrics`. These messages are distinct from API communication events.

Each content request has a UUID `request_id`. It stays the same through the
downloading Pi, its Piccolo lookup, and the serving Pi, using the
`X-Pi-Request-ID` header. Background API operations get their own IDs; image
advertisement and full sync share the ID of the state operation that started
them. Updated Pis on both ends are needed to see the ID in both sets of logs.

At `INFO`, Pi records request summaries, peer download outcomes, and API results.
At `DEBUG`, it also records request starts, peer attempts, API retries, image
events, timer activity, and full key lists. Health checks are omitted.

For example, these abbreviated lines describe one P2P download (the upload
line is on the serving Pi):

```text
component=piccolo operation=findkey event=api_request_finished request_id=<id> result=ok status=200 peer_count=1 peers=[10.0.0.2:5127]
component=upload event=request_finished request_id=<id> peer=10.0.0.1:45678 handler=blob status=200 bytes_sent=1048576 latency=1.2s
component=download event=peer_download_finished request_id=<id> peer=10.0.0.2:5127 status=200 bytes_sent=1048576 latency=1.3s
component=download event=request_finished request_id=<id> result=hit status=200 bytes_sent=1048576 latency=1.4s
```

- Upload `peer` is the TCP caller's address; download `peer` is the serving Pi's
  advertised address. `client_ip` preserves the original requester when forwarded.
- `bytes_sent` counts bytes written to the peer (`upload`) or containerd
  (`download`). HEAD requests send zero body bytes. Range responses include
  `content_range` and count only the bytes sent in that response.
- `result=latest_tag_skipped` and `result=miss` on download requests are normal
  `INFO` outcomes with HTTP 404. A Piccolo `findkey` 404 is also an `INFO` miss.
  Other API failures, including an advertise/sync endpoint returning 404, are
  logged at `ERROR`.
- A download miss lets containerd try another mirror or the upstream registry.
  Pi logs cannot confirm the outcome of that subsequent upstream download.

Filter by component or follow a request across log files:

```sh
rg 'component=upload' pi.log
rg 'component=download' pi.log
rg 'component=piccolo' pi.log
rg 'request_id=847cd673-878a-4fd1-b267-03688ca43c98' pi-*.log
```
