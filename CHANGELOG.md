# Changelog

Highlights from tagged snapshots and commit history; early patch releases are grouped.

## v0.2.2 — 2026-09-15

- Prevent Pi self-downloads by excluding the requester IP, across all ports, from Piccolo's `findkey` results before peer selection and count limiting.
- Return 404 when only the requester holds the key, and log the requester IP and excluded holder count.

Upgrade the Piccolo server to enable this fix; existing Pis already send the required `request_host` parameter.

## v0.2.1 — 2026-09-15

- Support multiple containerd namespaces for image discovery, sync, and serving.
- Add upload/download/Piccolo log categories and request IDs across peers.
- Fix `latest` handling for registries with ports, peer retries, partial-content responses, and missing-blob errors.

## v0.2.0 — 2026-09-15

- Default full refresh to 60 minutes and reject invalid intervals.
- Fix premature HTTP response cancellation and response status tracking.
- Run Go tests with the race detector in GitHub Actions.

## v0.1.1 — 2026-09-02

- Fix wrong-architecture image pulls on mixed-architecture clusters with architecture-scoped tag lookup and manifest validation.

## v0.1.0 — 2026-09-02

- Combine the v0.0.37 database/peer improvements with v0.0.40 cross-platform image builds.

## v0.0.40 — 2026-08-11

- Add Docker builds using `TARGETOS` and `TARGETARCH`, based on the v0.0.30 branch.

## v0.0.37 — 2026-03-11

- Prefer the peer with the closest IPv4 prefix when resolving image holders.

## v0.0.31–v0.0.36 — 2025-12-11 to 2025-12-16

- Add group-based database routing, multi-database migrations, and primary-database writes.
- Make dead-host cleanup configurable across primary databases; add keepalive metrics and database diagnostics.

## v0.0.5–v0.0.30 — 2025-10-15 to 2025-11-28

- Establish containerd-backed P2P image serving, discovery, sync, and upload limits.
- Add keepalives, dead-host cleanup, database read/write splitting, metrics, and profiling.
- Improve batching, indexes, retries, timeouts, and containerd subscription cleanup.
