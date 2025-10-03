rsync:
	rsync -avh --progress . /Users/xintao.lai/Programs/machines/foobar/piccolo-vm

debug:
	go run main.go \
		--containerd-sock /var/run/containerd/containerd.sock \
		--containerd-namespace k8s.io \
		--containerd-content-path /var/lib/containerd/io.containerd.content.v1.content \
		--registries '${REGISTRY_DOMAIN_LIST}' \
		--log-level DEBUG
