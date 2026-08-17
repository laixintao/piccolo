# HTTP API

The Piccolo API defaults to `http://0.0.0.0:7789`. All request and response bodies are JSON.

## Health and metrics

```bash
curl --fail http://127.0.0.1:7789/healthz
curl --fail http://127.0.0.1:7789/metrics
```

## Advertise keys

Adds key mappings without removing existing mappings for the holder.

```bash
curl --fail-with-body \
  -X POST http://127.0.0.1:7789/api/v1/distribution/advertise \
  -H 'Content-Type: application/json' \
  -d '{
    "holder": "10.0.0.12:5001",
    "group": "default",
    "keys": [
      "docker.io/library/alpine:latest",
      "sha256:0123456789abcdef"
    ]
  }'
```

A successful insert returns `201 Created`:

```json
{"success":true,"message":"Distribution created!"}
```

## Synchronize a holder

Reconciles the holder's stored keys with the supplied complete set. Mappings omitted from the request are deleted.

```bash
curl --fail-with-body \
  -X POST http://127.0.0.1:7789/api/v1/distribution/sync \
  -H 'Content-Type: application/json' \
  -d '{
    "holder": "10.0.0.12:5001",
    "group": "default",
    "keys": ["sha256:0123456789abcdef"]
  }'
```

## Find a key

```bash
curl --get --fail-with-body \
  http://127.0.0.1:7789/api/v1/distribution/findkey \
  --data-urlencode 'group=default' \
  --data-urlencode 'key=sha256:0123456789abcdef' \
  --data-urlencode 'count=10' \
  --data-urlencode 'request_host=10.0.0.25'
```

Example response:

```json
{
  "key": "sha256:0123456789abcdef",
  "group": "default",
  "holders": ["10.0.0.12:5001", "10.0.1.8:5001"],
  "total": 2
}
```

`count` defaults to 100. `request_host` is optional; when supplied, the IPv4 holder with the longest matching address prefix is returned first. A missing key returns `404 Not Found`.

## Keepalive

```bash
curl --fail-with-body \
  -X POST http://127.0.0.1:7789/api/v1/keepalive \
  -H 'Content-Type: application/json' \
  -d '{"host":"10.0.0.12:5001","group":"default"}'
```

The evictor uses keepalive timestamps to remove stale holder and distribution records.

## Error handling

- `400` indicates invalid JSON or missing required input.
- `404` indicates that a key or OCI resource could not be found.
- `500` indicates a storage or internal service failure.
- `503` on a Pi peer listener indicates its upload concurrency limit has been reached.

Clients should use bounded retries with exponential backoff for transient `5xx` responses.
