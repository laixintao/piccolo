rsync:
	rsync -avh --progress . /Users/xintao.lai/Programs/machines/foobar/piccolo-vm

debug:
	go run main.go \
		--containerd-sock /var/run/containerd/containerd.sock \
		--containerd-namespace k8s.io \
		--containerd-content-path /var/lib/containerd/io.containerd.content.v1.content \
		--registries '${REGISTRY_DOMAIN_LIST}' \
		--log-level DEBUG

build-api:
	go build -ldflags "-X main.version=v0.0.4 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o piccolo cmd/piccolo/main.go
