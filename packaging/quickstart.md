# OneSync quickstart

This guide is for a small private acceptance test with two computers.

For formal acceptance runs, copy `packaging/acceptance-report.md` and fill it in while testing.
Before starting a run, go through `packaging/preflight-checklist.md`.

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

For acceptance runs, build all test binaries and checksums in one step:

```sh
packaging/build-acceptance.sh dist/acceptance
```

Copy the needed binaries from `dist/acceptance` to the source, target, and Relay computers. Record `dist/acceptance/SHA256SUMS.txt` in the acceptance report.

To create ready-to-copy Windows and Linux acceptance packages:

```sh
packaging/package-acceptance.sh dist/acceptance dist/acceptance-packages
```

Copy the Windows zip to Windows source or target computers. Copy the Linux tar.gz to Linux source, target, or Relay computers. Record `dist/acceptance-packages/PACKAGE-SHA256SUMS.txt` in the acceptance report.
Each package also includes `preflight-checklist.md`.

The Windows package includes `OneSync.exe` for normal use. Double-click it on source or target computers; helper `.cmd` scripts are only for debugging with package-local data and logs. OneSync automatically prepares and loads the source TLS certificate when the source service starts, then the web page lets you choose or type the endpoint used in the link:

- Windows source: double-click `OneSync.exe`.
- Windows target: double-click `OneSync.exe`, then paste the generated link.
- Linux source: run `./start-source.sh`.
- Linux target: run `./start-target.sh`, then paste the generated link.
- Linux Relay: `./make-relay-cert.sh`, then `./start-relay.sh`.

On Windows, `OneSync.exe` runs as a tray application. Closing or minimizing the browser page does not stop synchronization. Use the tray icon to open OneSync again or exit the service.

For Linux service-style testing, use the included control commands from the extracted Linux package:

```sh
sudo ./onesyncctl install
sudo onesyncctl start
sudo onesyncctl status
sudo onesyncctl logs
sudo onesyncctl stop
sudo onesyncctl restart
sudo onesyncctl upgrade
sudo onesyncctl uninstall
```

The Linux service listens on `0.0.0.0:8765` by default and requires a management account. Open `http://server-ip:8765`, then set the account and password on first access. To keep it local-only instead, install with `sudo ONESYNC_WEB_BIND=127.0.0.1 ./onesyncctl install`.

For a Linux Relay server, use the one-command Relay TLS deployment script. `RELAY_HOSTS` is the Relay server domain or public IP without the port, and `RELAY_PORT` is the port clients will use:

```sh
curl -fsSL https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=203.0.113.10 RELAY_PORT=443 sh
```

After deployment, enter this Relay TLS address when generating a synchronization link:

```text
203.0.113.10:443
```

If you already downloaded the Linux package, you can also install Relay from the extracted directory:

```sh
sudo RELAY_HOSTS=relay.example.com,203.0.113.10 ./onesync-relayctl install
sudo RELAY_HOSTS=203.0.113.10 RELAY_PORT=443 ./onesync-relayctl install
sudo onesync-relayctl start
sudo onesync-relayctl status
sudo onesync-relayctl logs
sudo onesync-relayctl stop
sudo onesync-relayctl restart
sudo onesync-relayctl upgrade
sudo onesync-relayctl uninstall
```

`RELAY_HOSTS` is written into the Relay TLS certificate. It should contain the Relay domain or public IP, without the port. `RELAY_PORT` controls the listening port. When creating a synchronization link, enter the Relay TLS address as `host:port`, for example `203.0.113.10:443`.

By default, `onesync-relayctl install` generates a private self-signed Relay TLS certificate under `/etc/onesync/relay.crt` and `/etc/onesync/relay.key`. To use your own certificate, install with `ONESYNC_RELAY_CERT=/path/fullchain.crt ONESYNC_RELAY_KEY=/path/private.key`.

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

## Source TLS certificate

For normal source and target acceptance, do not manually create or copy source certificate files. When OneSync starts without `-cert` and `-key`, it automatically writes and loads a source TLS certificate under the OneSync data directory. The generated synchronization link carries the public certificate to the target.

If the source LAN IP changes, restart OneSync on the source computer. The automatic certificate will be checked against the current private IPv4 addresses and refreshed when needed. The link endpoint still needs to use the address that the target can reach.

## Direct connection

On the source computer:

```sh
onesync -sync-interval 10s
```

Open the management page. On Windows 10 or newer it opens automatically. On Linux, open `http://127.0.0.1:8765` locally, or use a trusted SSH tunnel.

If the source task card or link dialog says the source TLS certificate did not load automatically, restart OneSync. For Relay-only testing, fill the Relay TLS address before generating the link.

OneSync rejects links that have neither a usable source certificate nor a Relay address.

If the link dialog says the source certificate does not contain the endpoint host, choose one of the "证书地址" buttons when it is reachable from the target computer, or restart OneSync on the source computer so the automatic certificate refreshes for the current network.

Create a source task and choose the folder to send. Click "生成链接并启动". In the dialog, first try a "证书地址" suggestion that the target computer can reach. If it is not reachable, enter another source synchronization endpoint manually, for example:

```text
192.168.1.10:7443
```

Suggested endpoints use port `7443` by default. Start `onesync` with `-sync-port 9443` if you want the management page to suggest a different synchronization port.

Leave the Relay address empty for direct mode. Click "生成链接并启动源端", then copy the generated link. The source task starts automatically and waits for the target computer.

On the target computer:

```sh
onesync -sync-interval 10s
```

Open the management page, paste the link, and click "测试连接" before joining. This checks the direct TLS endpoint with the public certificate carried in the link. It does not consume the link or create a task.

Choose the target folder and click "加入并启动". The target will create and update files from the source. Files that exist only on the target are kept.

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
onesync-relay -listen :443 -cert relay.crt -key relay.key
```

The Relay certificate must contain the Relay address used by source and target computers. Use a public CA certificate for a public production Relay when possible. For private testing, a self-signed Relay certificate is acceptable; OneSync reads the Relay public certificate while generating the synchronization link and carries it in the link automatically.

When generating the source link, keep the source TLS endpoint as the direct endpoint and enter the Relay TLS address in the optional Relay field, for example:

```text
relay.example.com:7443
203.0.113.10:443
```

Click "生成链接并启动源端" before testing the link on the target computer. In Relay mode, the source task registers with Relay and waits for the matching target.

On the target computer, paste the synchronization link. The link already contains the public certificates needed for direct and Relay TLS verification. "测试连接" checks both the direct source endpoint and the Relay TLS endpoint when Relay is present. The target first tries the direct source endpoint. If it cannot connect or authenticate directly, it falls back to Relay.

For Relay acceptance, keep the direct endpoint field filled with the source address shown in the link form, and fill the Relay field with the reachable Relay TLS address. The target first tries direct mode, then falls back to Relay when direct mode is not usable.

## Troubleshooting

If "测试连接" fails for direct mode:

- Confirm the source task is started.
- Confirm the link endpoint is the synchronization endpoint, such as `192.168.1.10:7443`, not `127.0.0.1:8765`.
- Confirm the source firewall allows the synchronization port.
- Confirm the target is using a link generated by the source after OneSync started.
- Confirm the link dialog uses a "证书地址" suggestion that the target can reach, or restart the source after the source IP changes.

If "测试连接" fails for Relay:

- Confirm `onesync-relay` is running with `-cert` and `-key`.
- Confirm the Relay address in the link is reachable from both source and target.
- Confirm the Relay certificate contains the exact Relay address used in the link. If the Relay IP or domain changes, regenerate the Relay certificate and generate a fresh synchronization link.
- If direct mode is intentionally unavailable, a direct failure can be acceptable as long as the Relay result is usable.

If a task starts and then fails:

- Check the latest error on the task card.
- If the task says its credential is missing, generate a link for the source task before starting it. For a target task, join the synchronization link again.
- Check that source and target folders exist and are writable by the running user.
- Check that the link has not expired. Links are valid for 24 hours and are consumed by the first successful target.
- If the first target already joined successfully, generate a fresh link for another target.

## Security notes

- TLS 1.3 is mandatory for direct and Relay traffic.
- Certificate verification is mandatory; there is no "skip verification" mode.
- Do not share `.key` files.
- Share synchronization links only through a trusted channel. A link is valid for 24 hours and binds to the first target device that successfully authenticates.
- The management page binds to `127.0.0.1`. Do not expose it directly to a public network.
