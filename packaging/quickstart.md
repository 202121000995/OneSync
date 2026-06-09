# OneSync quickstart

This guide is for a small private acceptance test with two computers.

OneSync has two different local addresses:

- Management page: `http://127.0.0.1:8765`, only opened on the same computer.
- Synchronization endpoint: a TLS address other computers can reach, such as `192.168.1.10:7443`.

The management page address is not the synchronization endpoint.

## Before you start

Pick the source computer first. The source computer sends files, and the target computer receives files.

Write down these values before creating links:

- Source LAN IP: for example `192.168.1.10`.
- Synchronization port: `7443` by default, or the value passed with `-sync-port`.
- Source folder: the folder to send.
- Target folder: the folder that receives files.
- Optional Relay address: for example `relay.example.com:7443`.

Make sure the target computer can reach the source synchronization endpoint. If you use direct mode, the source computer firewall must allow the synchronization port. The management page port `8765` does not need to be reachable from the other computer.

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

If the source LAN IP changes, generate a new certificate that includes the new IP address. Certificate verification is strict, so a certificate for `192.168.1.10` will not verify when the target connects to `192.168.1.25`.

## Direct connection

On the source computer:

```sh
onesync -cert source.crt -key source.key -ca source.crt -sync-interval 10s
```

Open the management page. On Windows 10 or newer it opens automatically. On Linux, open `http://127.0.0.1:8765` locally, or use a trusted SSH tunnel.

If the source task card or link dialog says the source TLS certificate is not loaded, stop `onesync` and restart it with both `-cert source.crt` and `-key source.key`. Direct synchronization will not listen without those two files.

If the link dialog says the source certificate does not contain the endpoint host, choose one of the "证书地址" buttons when it is reachable from the target computer, or regenerate the certificate with the IP address or DNS name that the target computer will use.

Create a source task and choose the folder to send. Click "生成链接". In the dialog, first try a "证书地址" suggestion. If it is not reachable from the target computer, choose a suggested private IPv4 endpoint or enter the source synchronization endpoint manually, for example:

```text
192.168.1.10:7443
```

Suggested endpoints use port `7443` by default. Start `onesync` with `-sync-port 9443` if you want the management page to suggest a different synchronization port.

Leave the Relay address empty for direct mode. Copy the generated link, then start the source task. The source task must be running before the target computer can test or join the direct endpoint.

On the target computer:

```sh
onesync -ca source.crt -sync-interval 10s
```

Open the management page, paste the link, and click "测试连接" before joining. This checks the direct TLS endpoint with the target computer's current `-ca` trust configuration. It does not consume the link or create a task.

Choose the target folder and join. Start the target task. The target will create and update files from the source. Files that exist only on the target are kept.

Expected result:

- The source task moves through "连接中" and "同步中", then returns to "等待下一轮".
- The target folder receives files from the source folder.
- If a file exists at the same relative path on both computers and the contents differ, the source version wins.
- Files that exist only on the target are not deleted.

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

Start the source task before testing the link on the target computer. In Relay mode, the source task registers with Relay and waits for the matching target.

On the target computer, "测试连接" checks both the direct source endpoint and the Relay TLS endpoint when Relay is present. The target first tries the direct source endpoint. If it cannot connect or authenticate directly, it falls back to Relay.

For Relay-only acceptance, the source can run without `-cert` and `-key` as long as the generated link has a Relay address. The management page will warn that direct synchronization will not listen; that is expected for Relay-only use. Keep the direct endpoint field filled with the source address shown in the link form, and fill the Relay field with the reachable Relay TLS address.

## Troubleshooting

If "测试连接" fails for direct mode:

- Confirm the source task is started.
- Confirm the source was started with `-cert` and `-key`.
- Confirm the link endpoint is the synchronization endpoint, such as `192.168.1.10:7443`, not `127.0.0.1:8765`.
- Confirm the source firewall allows the synchronization port.
- Confirm the target was started with `-ca source.crt` or a CA bundle that includes `source.crt`.
- Confirm the certificate includes the exact IP address or DNS name used in the link endpoint.
- If the source management page warns that the certificate does not contain the endpoint host, fix that warning before testing from the target computer.

If "测试连接" fails for Relay:

- Confirm `onesync-relay` is running with `-cert` and `-key`.
- Confirm the Relay address in the link is reachable from both source and target.
- Confirm both source and target trust the Relay certificate through `-ca`.
- If direct mode is intentionally unavailable, a direct failure can be acceptable as long as the Relay result is usable.

If a task starts and then fails:

- Check the latest error on the task card.
- Check that source and target folders exist and are writable by the running user.
- Check that the link has not expired. Links are valid for 24 hours and are consumed by the first successful target.
- If the first target already joined successfully, generate a fresh link for another target.

## Security notes

- TLS 1.3 is mandatory for direct and Relay traffic.
- Certificate verification is mandatory; there is no "skip verification" mode.
- Do not share `.key` files.
- Share synchronization links only through a trusted channel. A link is valid for 24 hours and binds to the first target device that successfully authenticates.
- The management page binds to `127.0.0.1`. Do not expose it directly to a public network.
