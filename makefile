rsync:
	rsync -avh --progress . /Users/xintao.lai/Programs/machines/foobar/piccolo-vm

debug:
	go run main.go \
		--containerd-sock /var/run/containerd/containerd.sock \
		--containerd-namespace k8s.io \
		--containerd-content-path /var/lib/containerd/io.containerd.content.v1.content \
		--registries '${REGISTRY_DOMAIN_LIST}' \
		--log-level DEBUG

COMMIT := $(shell git rev-parse --short HEAD)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
build-api:
	go build -ldflags "-X main.version=v0.0.28 -X main.commit=${COMMIT} -X main.date=${DATE}" -o piccolo cmd/piccolo/main.go
