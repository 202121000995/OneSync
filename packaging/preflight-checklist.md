# OneSync preflight checklist

Use this checklist before a real multi-machine acceptance run. Fill the blanks first, then run the checks in order.

## 1. Test plan

- [ ] Source computer:
- [ ] Target computer:
- [ ] Relay computer, if used:
- [ ] Source folder:
- [ ] Target folder:
- [ ] Direct source endpoint, for example `192.168.1.10:7443`:
- [ ] Relay endpoint, if used:
- [ ] Package file and SHA-256 checked against `PACKAGE-SHA256SUMS.txt`:

## 2. Unpack and scripts

- [ ] Windows computers use the Windows zip.
- [ ] Linux computers use the Linux tar.gz.
- [ ] Source and target packages are from the same commit.
- [ ] Source OneSync was started once so it can automatically prepare the source TLS certificate.
- [ ] Logs folder exists or will be created by the starter scripts.

## 3. Direct mode

Before generating the source link:

- [ ] Confirm the source LAN IP from the target computer's network, not from the source computer alone.
- [ ] Confirm the synchronization port. Default is `7443`.
- [ ] Confirm the source firewall allows the synchronization port.
- [ ] Confirm the management page port `8765` is only opened locally on each computer.
- [ ] If the source has multiple network cards, note which private IPv4 address the target should use.
- [ ] Link dialog "证书地址" includes the endpoint chosen for the target; if the source IP changed, restart source OneSync and reopen the dialog.
- [ ] Target is using the generated link that includes the source public certificate.
- [ ] Source link was generated with "生成链接并启动源端" before target clicks "测试连接".

Before joining on the target:

- [ ] The link endpoint is the synchronization endpoint, not `127.0.0.1:8765`.
- [ ] The link dialog has no certificate mismatch warning for the chosen endpoint.
- [ ] Target "测试连接" direct result is usable.

## 4. Relay mode

Before starting Relay:

- [ ] Relay certificate includes the DNS name or IP address used in the Relay endpoint.
- [ ] Relay started with both `-cert` and `-key`.
- [ ] Relay has an access token, shown by `sudo onesync-relayctl token` when using the service script.
- [ ] Relay firewall allows its listen port.
- [ ] Source link generation can read the Relay public certificate. The target does not need a separate Relay certificate file when using the generated link.

Before joining through Relay:

- [ ] Source link includes the Relay endpoint.
- [ ] Source link includes the Relay token.
- [ ] Source link was generated with "生成链接并启动源端" before target clicks "测试连接".
- [ ] Target "测试连接" Relay result is usable.
- [ ] If direct mode is expected to fail, record that expectation in the acceptance report.

## 5. File scenarios

Prepare a small source folder before the first sync:

- [ ] One small text file.
- [ ] One nested folder with a file inside.
- [ ] One empty file.
- [ ] One larger file if the network is stable enough for the run.

Prepare target-only and conflict checks:

- [ ] Create one file that exists only on the target. It should remain after sync.
- [ ] Create one file with the same relative path on source and target but different content. The source version should win.

## 6. Evidence to keep

Keep these with the acceptance report:

- [ ] `PACKAGE-SHA256SUMS.txt`.
- [ ] `BUILD.txt`.
- [ ] Source log.
- [ ] Target log.
- [ ] Relay log, if used.
- [ ] Screenshot of source task status.
- [ ] Screenshot of target "测试连接" result.
- [ ] Screenshot or note of final target folder contents.

## 7. If a check fails

Do not immediately change several things at once. Record the first failing step and check in this order:

1. Is the source task started?
2. Is the endpoint the synchronization endpoint, not the management page?
3. Can the target reach the source or Relay port?
4. Does the certificate include the endpoint host?
5. Was the source link generated with the correct Relay token and current Relay certificate?
6. Was the link already consumed or expired?

After fixing one item, run "测试连接" again and record the result.
