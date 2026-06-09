# OneSync acceptance report

Use this template for real Windows/Linux multi-machine acceptance runs. Keep one copy per test round.

## Run summary

- Date:
- Tester:
- Result: Pass / Fail / Blocked
- OneSync commit:
- Source computer OS:
- Target computer OS:
- Relay computer OS:
- Starter scripts used: Yes / No
- Preflight checklist completed: Yes / No
- Source LAN IP:
- Synchronization port:
- Relay endpoint:

## Build artifacts

Record the exact binaries used during the run.
When using `packaging/build-acceptance.sh`, attach or paste `SHA256SUMS.txt`.
When using `packaging/package-acceptance.sh`, attach or paste `PACKAGE-SHA256SUMS.txt`.

| Program | Platform | Path or filename | Version/commit | SHA-256 |
| --- | --- | --- | --- | --- |
| onesync | Windows/Linux |  |  |  |
| onesync-cert | Windows/Linux |  |  |  |
| onesync-relay | Linux |  |  |  |

## Certificates

| Certificate | Generated on | Hosts/SANs | How it is trusted | Notes |
| --- | --- | --- | --- | --- |
| Automatic source TLS certificate |  |  | Carried in synchronization link | Source private key stays on source computer |
| relay.crt |  |  |  |  |
| onesync-ca.crt |  |  |  |  |

Notes:

- Confirm private keys were not copied to target computers.
- Confirm the generated link includes the source public certificate for direct mode.
- Confirm the source endpoint host appears in the source certificate.
- Confirm the Relay endpoint host appears in the Relay certificate.

## Commands

Source command:

```sh

```

Target command:

```sh

```

Relay command:

```sh

```

## Direct connection checklist

| Check | Expected | Result | Notes |
| --- | --- | --- | --- |
| Source management page opens locally | `http://127.0.0.1:8765` opens |  |  |
| Source certificate is loaded automatically | No source TLS warning |  |  |
| Link dialog certificate endpoint is available | "证书地址" suggestion appears |  |  |
| Link dialog endpoint matches certificate | No certificate mismatch warning |  |  |
| Source task starts | State becomes "连接中" or waits for target |  |  |
| Target "测试连接" succeeds | Direct result is usable |  |  |
| Target joins link | Target task is created |  |  |
| First sync completes | Target receives source files |  |  |
| Modified source file updates target | Source version wins |  |  |
| Target-only file remains | File is not deleted |  |  |
| Deleted source file does not delete target copy | Target copy remains |  |  |
| Multi-file sync completes | 100+ small files complete without missing files |  |  |
| Large file sync completes | Large file hash or size matches |  |  |
| Diagnostics package downloads | Zip contains diagnostics and service log tail |  |  |

## Relay checklist

| Check | Expected | Result | Notes |
| --- | --- | --- | --- |
| Relay starts | Logs show Relay listening |  |  |
| Relay token is configured | `onesync-relayctl token` shows the token used in link generation |  |  |
| Source link carries Relay certificate | Target does not need a separate Relay certificate file |  |  |
| Source link includes Relay endpoint and token | Link test checks Relay |  |  |
| Direct path unavailable or disabled | Direct may fail when expected |  |  |
| Relay fallback succeeds | Target sync completes through Relay |  |  |
| Relay-only source works without `-cert/-key` | Link generation requires Relay field |  |  |
| Relay restart during later cycle | Client logs show retry/recovery or a clear failure reason |  |  |

## Negative checks

| Check | Expected | Result | Notes |
| --- | --- | --- | --- |
| Wrong Relay token in generated link | Relay connection fails authentication |  |  |
| Wrong or stale Relay certificate in link | Relay "测试连接" fails certificate verification |  |  |
| Source certificate missing endpoint host | Link dialog warns before testing |  |  |
| Source without cert and without Relay | Link generation is rejected |  |  |
| Expired or consumed link | Join is rejected |  |  |
| Stopped source task | Direct test or join fails clearly |  |  |

## File set

Record the source files used for the run.

| Relative path | Size | Scenario | Expected target result |
| --- | --- | --- | --- |
|  |  | create |  |
|  |  | update |  |
|  |  | target-only | kept |

## Logs and evidence

- Source logs:
- Target logs:
- Relay logs:
- Diagnostics zip:
- Screenshots:
- Generated link time:
- Test connection output:

## Issues found

| ID | Severity | Area | Description | Repro steps | Status |
| --- | --- | --- | --- | --- | --- |
|  |  |  |  |  |  |

## Final decision

- Ship for next test stage: Yes / No
- Required fixes before next run:
- Optional follow-ups:
