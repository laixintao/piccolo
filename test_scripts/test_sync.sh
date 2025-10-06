curl http://127.0.0.1:7789/api/v1/distribution/sync -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "after-sync"
        ],
        "holder": "127.0.0.1:5123"
    }'
