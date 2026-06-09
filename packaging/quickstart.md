# OneSync quickstart

This guide is for a small private acceptance test with two computers.

OneSync has two different local addresses:

- Management page: `http://127.0.0.1:8765`, only opened on the same computer.
- Synchronization endpoint: a TLS address other computers can reach, such as `192.168.1.10:7443`.

The management page address is not the synchronization endpoint.

## Build

Build the three programs:

```sh
go build -o onesync ./cmd/onesync
go build -o onesync-relay ./cmd/relay
go build -o onesync-cert ./cmd/onesync-cert
```

For Windows and Linux release checks:

```sh
GOOS=windows GOARCH=amd64 go build -o onesync.exe ./cmd/onesync
GOOS=windows GOARCH=amd64 go build -o onesync-cert.exe ./cmd/onesync-cert
GOOS=linux GOARCH=amd64 go build -o onesync-linux ./cmd/onesync
GOOS=linux GOARCH=amd64 go build -o onesync-relay-linux ./cmd/relay
GOOS=linux GOARCH=amd64 go build -o onesync-cert-linux ./cmd/onesync-cert
```

## Create a local certificate

Generate a TLS certificate on the source computer. Include every name or IP address the target computer will use to connect.

```sh
onesync-cert -hosts 192.168.1.10,localhost,127.0.0.1 -cert source.crt -key source.key
```

Keep `source.key` private. Copy `source.crt` to the target computer and start both clients with `-ca source.crt` so the target trusts the source certificate.

The generated certificate is a local self-signed certificate for private testing or small trusted deployments. It is not a production certificate lifecycle system.

## Direct connection

On the source computer:

```sh
onesync -cert source.crt -key source.key -ca source.crt -sync-interval 10s
```

Open the management page. On Windows 10 or newer it opens automatically. On Linux, open `http://127.0.0.1:8765` locally, or use a trusted SSH tunnel.

Create a source task and choose the folder to send. Click "生成链接". In the dialog, choose a suggested private IPv4 endpoint or enter the source synchronization endpoint manually, for example:

```text
192.168.1.10:7443
```

Leave the Relay address empty for direct mode. Copy the generated link.

On the target computer:

```sh
onesync -ca source.crt -sync-interval 10s
```

Open the management page, paste the link, and click "测试连接" before joining. This checks the direct TLS endpoint with the target computer's current `-ca` trust configuration. It does not consume the link or create a task.

Choose the target folder and join. Start the source task and target task. The target will create and update files from the source. Files that exist only on the target are kept.

## Relay connection

Use Relay when the target cannot directly reach the source.

On the Relay server:

```sh
onesync-cert -hosts relay.example.com,203.0.113.10 -cert relay.crt -key relay.key
onesync-relay -listen :7443 -cert relay.crt -key relay.key
```

If the source certificate and Relay certificate are different self-signed certificates, put both public certificates into one CA bundle:

```sh
cat source.crt relay.crt > onesync-ca.crt
```

Start the source and target with the same CA bundle:

```sh
onesync -cert source.crt -key source.key -ca onesync-ca.crt -sync-interval 10s
onesync -ca onesync-ca.crt -sync-interval 10s
```

When generating the source link, keep the source TLS endpoint as the direct endpoint and enter the Relay TLS address in the optional Relay field, for example:

```text
relay.example.com:7443
```

On the target computer, "测试连接" checks both the direct source endpoint and the Relay TLS endpoint when Relay is present. The target first tries the direct source endpoint. If it cannot connect or authenticate directly, it falls back to Relay.

## Security notes

- TLS 1.3 is mandatory for direct and Relay traffic.
- Certificate verification is mandatory; there is no "skip verification" mode.
- Do not share `.key` files.
- Share synchronization links only through a trusted channel. A link is valid for 24 hours and binds to the first target device that successfully authenticates.
- The management page binds to `127.0.0.1`. Do not expose it directly to a public network.
