# Changelog

All notable changes to Piccolo will be documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and releases use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project overview, architecture, API, security, and contribution documentation.
- Portable Makefile, reproducible container builds, local Docker Compose stack, and GitHub Actions CI.
- Argument validation, graceful signal handling, HTTP server timeouts, and opt-in pprof endpoints.
- Tests for current containerd integration and HTTP retry behavior.

### Fixed

- Keep HTTP request contexts alive until successful response bodies are closed.
- Cancel containerd subscriptions on shutdown and avoid blocked event forwarding.
- Stop `/sync` after a database lookup failure instead of writing a second response.
- Populate `total` and the correct JSON field names in discovery responses.
- Restore compilation of the full test suite after removed OCI helpers left stale tests behind.
