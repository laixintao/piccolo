# Contributing

Thank you for improving Piccolo. Bug reports, documentation fixes, tests, and focused code changes are welcome.

## Before opening an issue

- Search existing issues and include the Piccolo version, Go version, containerd version, and operating system.
- For pull failures, include the upstream registry, the relevant containerd registry configuration, and sanitized Pi/Piccolo logs.
- Never post credentials, private image names, internal addresses, or database DSNs.
- Report security problems privately as described in [SECURITY.md](SECURITY.md).

## Development setup

Install Go 1.24.1 or newer, clone the repository, then run:

```bash
go mod download
make check
make build
```

The local discovery stack can be started with:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

## Pull requests

1. Keep each change focused and explain its user-visible impact.
2. Add tests for behavior changes and regression fixes.
3. Update the README or `docs/` when flags, endpoints, or deployment behavior change.
4. Run `make check` before submitting.
5. Use `gofmt` for all Go code and avoid unrelated formatting churn.

Commit messages should be short and imperative, for example `Fix request context lifetime`.

## Testing guidance

Unit tests must not require public network access. Integration tests that need containerd or MySQL should be clearly labeled and document their prerequisites. Prefer table-driven tests for parsers and request behavior.

## API compatibility

Piccolo is pre-1.0, but compatibility still matters. Call out breaking flag, environment variable, API, schema, or metrics changes in the pull request and in [CHANGELOG.md](CHANGELOG.md).

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
