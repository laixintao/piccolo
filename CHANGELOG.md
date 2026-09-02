# Changelog

All notable changes to Piccolo will be documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

This section describes the release currently being prepared after `v0.0.40`.

### Breaking changes

- Pi peer identities are now required to be literal IPv4 `IP:port` values. Hostnames, IPv6 addresses, and addresses without a port are rejected by the Pi CLI and by the advertise, sync, and keepalive APIs. Existing non-IPv4 holder rows should be removed before upgrading.
- `--piccolo-api` is now required and must contain an `http` or `https` scheme and a host. Relative and scheme-less Piccolo API addresses no longer start.
- Every `--registries` value is now validated during startup. Registry URLs must use HTTP(S), include a host, and must not contain a path, query string, or user information.
- Pi now rejects non-positive values for `--full-refresh-minutes`, `--max-upload-connections`, `--max-upload-blob-bytes-per-second`, `--mirror-resolve-timeout`, and `--mirror-resolve-retries`. The default full refresh interval is now 60 minutes.
- Go pprof endpoints are disabled by default on both binaries. Deployments that scrape or invoke `/debug/pprof/*` must explicitly set `--enable-pprof` or `ENABLE_PPROF=true`.
- The discovery `findkey` response serializes its group as `"group"` instead of `"Group"`. Clients that decode JSON field names case-sensitively must update. The new `"total"` field is additive.
- Several HTTP response details changed: an invalid `request_host` returns `400` instead of `404`; `/sync` returns the message `"Distribution synchronized!"`; internal OCI read failures return `500`, while missing OCI content returns `404`; and database error details are no longer included in API responses. Clients must not depend on the previous status or message text.
- Piccolo rejects request bodies larger than 16 MiB. Deployments that send very large advertise or full-sync payloads must reduce them before upgrading.
- The Pi container now uses `ENTRYPOINT ["./pi"]` with a default `--help` command. Workflows that replaced the old Docker `CMD` with another executable must use `--entrypoint`; normal Pi flags continue to work as container arguments.
- The Makefile no longer provides the local-only `rsync`, `debug`, or `build-api` targets. Builds now use `build-pi` and `build-piccolo`, and binaries are written to `bin/pi` and `bin/piccolo` rather than the repository root.
- The exported Go constant `registry.ONE_G_BPS` was removed. Go consumers must use their own rate value or configure the server through `WithMaxUploadBlobSpeedBytes`.

### Added

- Project overview, architecture, API, security, and contribution documentation.
- Portable Makefile, reproducible container builds, local Docker Compose stack, and GitHub Actions CI.
- Graceful SIGINT/SIGTERM handling and bounded shutdown for Piccolo, Pi, registry, peer, and metrics HTTP servers.
- Read-header and idle timeouts for HTTP listeners, plus a 16 MiB Piccolo request-body limit.
- Opt-in pprof endpoints controlled by `--enable-pprof` and `ENABLE_PPROF`.
- A startup image-state reconciliation that runs once after the first successful containerd event subscription, with up to 60 seconds of random jitter to spread load across Pi nodes.
- Validation for database roles, registry URLs, peer addresses, and positive Pi tuning values.
- Tests for containerd availability checks, local Pi health, startup synchronization metrics, HTTP retries, range responses, peer fallback, request validation, and shutdown-related behavior.

### Changed

- Containerd startup verification now checks only `IsServing` with a 10-second timeout. Pi no longer requires the containerd CRI plugin or calls the CRI RuntimeService during verification.
- Pi `/healthz` is a local process check and does not call Piccolo or any other remote service.
- Pi performs periodic full reconciliation every 60 minutes by default and records a subscription as successful only after `Subscribe` succeeds.
- Pi preserves an optional base path in `--piccolo-api` when constructing Piccolo API request URLs.
- `/sync` distinguishes an explicit empty `"keys": []` list, which clears a holder, from an omitted or `null` keys field, which is rejected.
- Peer selection extracts the request IP with `netip` instead of splitting the configured address as a string.
- Registry proxying accepts every successful 2xx response, including `206 Partial Content`, and tries another peer when a peer connection or response fails before committing output.
- Blob upload limiting now uses a shared 32 KiB burst, supports request cancellation, and avoids requests larger than the limiter burst.
- Database logging defaults to warnings, DSN lists and resolver names are deterministic, and migration logs no longer print DSNs.
- Docker builds use version metadata from `VERSION` and build arguments, produce stripped reproducible binaries, and use pinned Alpine runtime images.
- Piccolo supports explicit `server` and `migrate-db` subcommands while retaining the legacy server-flag invocation form.

### Fixed

- Keep HTTP request contexts alive until successful response bodies are closed.
- Bound error-body reads and close retryable server responses before retrying.
- Cancel containerd subscriptions on shutdown and avoid blocked event forwarding.
- Stop `/sync` after a database lookup failure instead of writing a second response.
- Allow an explicit empty sync to delete all mappings for a holder.
- Populate `total` and the correct JSON field names in discovery responses and keep keepalive group decoding consistent.
- Recompute both tag and digest image-reference gauges during startup and periodic full synchronization.
- Return `404` for missing manifests and blobs and `500` for internal OCI failures.
- Preserve buffer-pool capacity, suppress duplicate HTTP response headers, and classify all 2xx mirror responses as cache hits.
- Escape every regular-expression metacharacter in containerd registry filters.
- Validate containerd availability without depending on CRI runtime version strings.
- Restore compilation of the full test suite after removed OCI helpers left stale tests behind.

### Removed

- Unused containerd mirror-template helpers, stale tests for already-removed mirror configuration code, and the unused internal channel implementation.
- Direct dependencies on the semantic-version and Kubernetes CRI API modules.

### Security

- Disable pprof exposure by default.
- Limit Piccolo request bodies and HTTP error-body reads.
- Avoid returning raw database errors or logging database DSNs.
- Document that Piccolo has no built-in authentication or TLS and must be restricted to trusted networks or protected by an external proxy or service mesh.
