curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "asdf",
            "docker.io/laixintao//root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.24.1.linux-arm64/src/runtime/asm_arm64.s/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.24.1.linux-arm64/src/runtime/asm_arm64.sabc"
        ],
        "holder": "127.0.0.1:5123",
        "group": "internaltest"
    }'
