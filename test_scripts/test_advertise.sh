curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "asdf",
            "11asdfasdf"
        ],
        "holder": "127.0.0.1:5123"
    }'
