# Testing

Install Go at the version specified in `go.mod` or newer, plus a C compiler
for the race detector, then run from the repository root:

```sh
go test -race -count=1 -timeout=5m ./...
```

The Go tests use temporary directories, local OCI fixtures, and in-process
services. They do not require a running containerd daemon, MySQL database, or
access to an image registry. Coverage includes namespace CLI/environment list
parsing and precedence, OCI image traversal and
architecture selection, containerd namespace event filtering and content lookup,
registry request handling, log correlation between components, API retry and
cache-miss logging, buffer pooling, and HTTP response tracking.

GitHub Actions runs this command on Ubuntu for every pull request and push
to `main`. The Go version is read from `go.mod`. Test results are available in
the **Go tests / Test** check on each PR.

The scripts in `test_scripts/` exercise a running deployment and are not part
of this self-contained Go test suite.

See the [PR #18 Vagrant report](test_scripts/reports/2026-09-15-pr18-vagrant.md)
for completed three-machine results, fixes, and cleanup details.
