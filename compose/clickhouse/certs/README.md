# ClickHouse dev TLS certs

Self-signed material for the compose ClickHouse service. Used by the
`TestIntegrationClickHouseTLS` and `TestIntegrationClickHouseMTLS`
tests in `internal/db/clickhouse/clickhouse_integration_test.go`.

**Dev-only.** Do not reuse in production. 10-year validity so we don't
have to rotate these in the middle of a sprint.

## Files

- `ca.crt` / `ca.key` -- self-signed CA, signs both server and client
- `server.crt` / `server.key` -- clickhouse-server cert. SANs: localhost,
  127.0.0.1, sqlgo-clickhouse, clickhouse
- `client.crt` / `client.key` -- client cert used by mTLS tests
- `dhparam.pem` -- Diffie-Hellman params required by the clickhouse-server
  OpenSSL config block

## Regenerate

```bash
cd compose/clickhouse/certs
rm -f *.crt *.key *.pem *.srl

openssl genrsa -out ca.key 2048
openssl req -x509 -new -nodes -key ca.key -sha256 -days 3650 \
  -subj "/CN=sqlgo-dev-ca" -out ca.crt

openssl genrsa -out server.key 2048
openssl req -new -key server.key -subj "/CN=sqlgo-clickhouse" -out server.csr
printf "subjectAltName=DNS:localhost,DNS:sqlgo-clickhouse,DNS:clickhouse,IP:127.0.0.1\n" > server.ext
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -days 3650 -sha256 -extfile server.ext

openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=sqlgo-client" -out client.csr
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out client.crt -days 3650 -sha256

openssl dhparam -out dhparam.pem 2048
rm -f *.csr *.ext *.srl
```

On Windows MSYS/Git-Bash, prefix subjects with `//CN=...` instead of
`/CN=...` to avoid path translation mangling.
