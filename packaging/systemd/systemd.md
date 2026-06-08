# OneSync Linux systemd deployment

OneSync can run as a long-lived Linux service under systemd.

## Service user

Create a dedicated user:

```sh
sudo useradd --system --home /var/lib/onesync --shell /usr/sbin/nologin onesync
```

Install binaries:

```sh
sudo install -m 0755 onesync /usr/local/bin/onesync
sudo install -m 0755 onesync-relay /usr/local/bin/onesync-relay
```

Install unit files:

```sh
sudo install -m 0644 packaging/systemd/onesync.service /etc/systemd/system/onesync.service
sudo install -m 0644 packaging/systemd/onesync-relay.service /etc/systemd/system/onesync-relay.service
sudo systemctl daemon-reload
```

## Main service

Start the management and synchronization service:

```sh
sudo systemctl enable --now onesync.service
sudo systemctl status onesync.service
```

The management page listens on `127.0.0.1:8765`. For a headless server, open it through a trusted tunnel such as SSH port forwarding instead of binding it to a public address.

Logs are available through:

```sh
sudo journalctl -u onesync.service -f
sudo tail -f /var/log/onesync/onesync.log
```

## Relay service

Relay requires a TLS certificate and private key:

```sh
sudo install -m 0700 -d /etc/onesync
sudo install -m 0644 relay.crt /etc/onesync/relay.crt
sudo install -m 0600 relay.key /etc/onesync/relay.key
sudo systemctl enable --now onesync-relay.service
```

Relay logs are available through:

```sh
sudo journalctl -u onesync-relay.service -f
sudo tail -f /var/log/onesync/relay.log
```

## Stop and restart

```sh
sudo systemctl stop onesync.service
sudo systemctl restart onesync.service
sudo systemctl stop onesync-relay.service
sudo systemctl restart onesync-relay.service
```

Both services handle `SIGTERM` and shut down through the same cancellation path as interactive runs.
