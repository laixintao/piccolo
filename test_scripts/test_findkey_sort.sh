curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"10.23.145.7:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"10.198.4.221:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"172.16.58.109:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"192.168.12.34:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"10.77.0.156:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"172.31.200.5:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"192.168.88.199:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"10.5.66.42:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"172.20.33.250:5123","group":"localtest"}'
curl http://127.0.0.1:7789/api/v1/distribution/advertise -X POST -H "Content-Type: application/json" -d '{"keys":["foobar"],"holder":"192.168.120.7:5123","group":"localtest"}'

echo "------------test find key"
curl -sS 'http://127.0.0.1:7789/api/v1/distribution/findkey?key=foobar&count=5&group=localtest' -X GET | jq .

echo "------------test find key with sort"
curl -sS 'http://127.0.0.1:7789/api/v1/distribution/findkey?key=foobar&count=5&group=localtest&request_host=172.16.0.1' -X GET | jq .

echo "------------test find key with sort, request_host=172.16.0.1:5000"
curl -sS 'http://127.0.0.1:7789/api/v1/distribution/findkey?key=foobar&count=5&group=localtest&request_host=172.16.0.1:5000' -X GET | jq .
