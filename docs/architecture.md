# Architecture

Piccolo separates discovery metadata from OCI content. The central service stores only key-to-node mappings in MySQL; each Pi agent reads and serves content already present in its local containerd store.

## Components

### Piccolo API

The `piccolo` binary provides the discovery control plane:

- accepts advertisements and full-state syncs from Pi agents;
- resolves a tag or digest to candidate Pi peers;
- tracks node keepalives and optionally evicts stale records;
- supports named database groups and master/replica routing;
- exports Prometheus metrics.

Groups are isolation boundaries. A Pi only discovers peers that advertise to the same group.

### Pi agent

The `pi` binary runs one process per containerd node and starts three listeners:

- the **registry listener** is configured as containerd's local mirror;
- the **peer listener** serves local manifests and blobs to other Pi agents;
- the **metrics listener** exposes Prometheus metrics and, when explicitly enabled, pprof.

Pi subscribes to containerd image events for fast incremental updates. A periodic full reconciliation repairs missed events, and keepalives let Piccolo identify departed agents.

## Pull flow

1. containerd asks its local Pi registry listener for a manifest or blob.
2. Pi derives a discovery key from the OCI request.
3. Pi queries the Piccolo API for nodes in the same group that hold the key.
4. Pi tries candidate peer listeners in order.
5. If no peer succeeds, Pi proxies the request to the original registry.
6. Once containerd stores the image, Pi advertises the new keys to Piccolo.

The upstream request carries the standard containerd `ns` query parameter. Piccolo never proxies layer content.

## Consistency model

Discovery is eventually consistent:

- image create and update events are advertised incrementally;
- deletion events trigger a debounced full sync;
- full syncs replace the complete key set for a holder;
- failed nodes remain discoverable until the evictor removes them, but Pi falls back to other peers or upstream when a connection fails.

This design favors availability. A stale mapping can add a failed peer attempt, but it does not make the image unavailable while the upstream registry remains reachable.

## Data model

`distribution_tab` maps `(group, key, holder)` tuples. `host_tab` stores `(group, host_addr)` and the last keepalive time. Piccolo uses unique indexes to make repeated advertisements idempotent.

## Trust boundaries

Piccolo assumes a trusted cluster network. There is no built-in authentication or TLS. Place all listeners behind network policy, firewalls, a reverse proxy, or a service mesh as appropriate. pprof is disabled by default.
