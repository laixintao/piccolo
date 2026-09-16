# Containerd namespaces

Pi defaults to the `k8s.io` namespace. To discover and serve images from both
Kubernetes and workloads using `default`, set:

```sh
--containerd-namespace k8s.io default
```

Like `--registries`, this flag accepts a list of space-separated arguments.
The environment variable uses go-arg's comma-separated list format, like
`REGISTRIES`:

```sh
CONTAINERD_NAMESPACE=k8s.io,default
```

The existing single-namespace argument and environment variable remain valid.
For multiple namespaces on the command line, pass separate values rather than
`--containerd-namespace=k8s.io,default`.
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

# Peer discovery

Pi sends the IP from `--pi-listen-addr` as `request_host` when calling Piccolo's
`GET /api/v1/distribution/findkey` endpoint. The API excludes all holders with
that IP, including holders using a different port, before randomly sampling
distinct holders and applying `count`. The first peer is random too: Pi tries
peers in response order, so always putting the nearest IP first concentrates
downloads on that machine. Selection stays within the requested group and no
longer ranks candidates by IPv4 prefix.

If no candidates remain after exclusion, the API returns HTTP 404 and Pi treats
the lookup as a miss. Requests without `request_host` also use random selection
but do not exclude any IP. This behavior requires updating the Piccolo server;
existing Pis already send `request_host` and need no changes.

## Rotating candidate pools

Each API process caches up to 2,000 holders per `(group, key)`. Every request
samples its own peers from that pool without modifying the cached ordering.
When a pool expires, the next request advances through the existing
`(group, key, holder)` index using `holder > last_holder ORDER BY holder LIMIT ...`.
At the end, another bounded query fills the window from the beginning, with an
upper bound preventing duplicate holders. No full scan, random SQL sort, or
large OFFSET is needed for a refresh.

Piccolo server settings (all must be positive):

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--peer-cache-max-keys` | `PEER_CACHE_MAX_KEYS` | `1024` |
| `--peer-cache-max-holders` | `PEER_CACHE_MAX_HOLDERS` | `100000` |
| `--peer-cache-refresh-interval` | `PEER_CACHE_REFRESH_INTERVAL` | `10s` |

The holder limit counts entries across **all** cached pools, including the same
holder appearing in multiple keys. A limit below 2,000 also reduces each pool's
size. The least recently used pools are evicted to satisfy both limits.
Eviction or API restart discards a pool's cursor; its next lookup starts at the
beginning. These limits bound retained candidate data, not total process memory
or temporary data for in-flight requests.

Refresh happens on demand after 80-100% of the configured interval (8-10 seconds
by default); cache hits do not extend it. Concurrent refreshes of the same pool
share one query sequence. Callers can cancel independently; the shared query
has a two-second timeout. Failed refreshes return an error and retain the old
cursor for retry. Empty results are not cached, so newly advertised keys can be
queried immediately.

This provides random selection within the current pool and rotation across all
holders over time, rather than uniform sampling from the entire database on
every request. For a continuously requested key with 100,000 holders, 50 windows
cover one pass while its pool remains cached. Withdrawn holders can remain in a
pool until refresh, and new holders behind the cursor join on a later pass.
Database replication lag can further delay visibility. Pi's existing peer
retries handle candidates that no longer serve the content.

API processes maintain independent cursors that all start at the beginning.
Multiple API nodes can therefore query overlapping or identical holder windows,
especially after startup or cache eviction. Refresh jitter changes timing but
does not guarantee distinct ranges across API nodes; cross-node range spreading
is deferred.

The `found holders for key` log includes `cache_result` (`hit`, `refresh`, or
`shared_refresh`), `candidate_count`, `excluded_self_count`, `selection=random`,
and `returned_count`. Candidate count describes the current pool, not the total
number of holders in the database.

# Advertisement deduplication

Pi keeps an in-memory cache of successfully advertised keys for five hours.
Repeated image events send only keys missing from the cache or whose entries
have expired. If every key is cached, Pi skips the HTTP request entirely. Cache
hits do not extend the expiry, and failed advertisements remain retryable.
The cache starts empty when Pi restarts.

The cache holds at most **100,000 keys** by default. To change the positive limit:

```sh
--advertise-cache-max-keys 50000
# Or use the environment variable:
ADVERTISE_CACHE_MAX_KEYS=50000
```

At capacity, the least recently used key is evicted. Evicted keys can be
advertised again before five hours pass. Expired entries are reclaimed lazily;
the limit applies to both advertisement and full-sync cache updates.

Full sync always sends the complete current key set. A successful sync refreshes
the cache with up to the configured limit of those keys and removes entries for
withdrawn keys, so a deleted image can be advertised again when pulled. A failed
sync clears the cache because the server may have partially changed its records.
Concurrent advertisements and syncs are serialized; callers can cancel while
waiting. This behavior requires updating Pi.

At DEBUG, `component=piccolo` logs `advertise_skipped`, `advertise_filtered`, and
`advertise_cache_evicted`, including skipped or evicted key counts. The
`key_count` on an actual advertise API request counts only the keys being sent.

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
