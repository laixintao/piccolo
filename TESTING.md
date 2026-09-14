# Testing

Install Go at the version specified in `go.mod` or newer, plus a C compiler
for the race detector, then run from the repository root:

```sh
go test -race -count=1 -timeout=5m ./...
```

The Go tests use temporary directories, local OCI fixtures, and in-process
services. They do not require a running containerd daemon, MySQL database, or
access to an image registry. Coverage includes OCI image traversal and
architecture selection, registry request handling, buffer pooling, and HTTP
response tracking.

GitHub Actions runs this command on Ubuntu for every pull request and push
to `main`. The Go version is read from `go.mod`. Test results are available in
the **Go tests / Test** check on each PR.

The scripts in `test_scripts/` exercise a running deployment and are not part
of this self-contained Go test suite.
