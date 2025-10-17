curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "asdf",
            "11asdfasdf"
        ],
        "group": "foo",
        "holder": "127.0.0.1:5123"
    }'

curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "asdf",
            "11asdfasdf"
        ],
        "group": "foo",
        "holder": "192.168.0.1:5123"
    }'

for i in {1..100}; do
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST \
    -H "Content-Type: application/json" \
    -d '{
        "keys": [
            "asdf",
            "11asdfasdf"
        ],
        "group": "foo",
        "holder": "192.168.0.'$i':5123"
    }'
done
