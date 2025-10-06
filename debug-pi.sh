go run cmd/pi/main.go \
    --containerd-sock /var/run/containerd/containerd.sock \
    --containerd-namespace k8s.io \
    --containerd-content-path /var/lib/containerd/io.containerd.content.v1.content \
     --registries  \
    http://harbor.shopeemobile.com  \
    http://harbor.test.shopeemobile.com  \
    --log-level DEBUG \
    --resolve-latest-tag=false \
    --full-refresh-minutes 5 \
    --max-upload-connections 10 \
    --pi-listen-addr 0.0.0.0:5000 \
    --registry-listen-addr 0.0.0.0:8111 \
    --piccolo-api http://example.com | cut -c-200
