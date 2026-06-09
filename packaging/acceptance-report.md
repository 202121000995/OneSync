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
- Source LAN IP:
- Synchronization port:
- Relay endpoint:

## Build artifacts

Record the exact binaries used during the run.
When using `packaging/build-acceptance.sh`, attach or paste `SHA256SUMS.txt`.

| Program | Platform | Path or filename | Version/commit | SHA-256 |
| --- | --- | --- | --- | --- |
| onesync | Windows/Linux |  |  |  |
| onesync-cert | Windows/Linux |  |  |  |
| onesync-relay | Linux |  |  |  |

## Certificates

| Certificate | Generated on | Hosts/SANs | Copied to | Used as `-ca` by |
| --- | --- | --- | --- | --- |
| source.crt |  |  |  |  |
| relay.crt |  |  |  |  |
| onesync-ca.crt |  |  |  |  |

Notes:

- Confirm private keys were not copied to target computers.
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
| Source certificate is loaded | No "TLS certificate is not loaded" warning |  |  |
| Link dialog certificate endpoint is available | "证书地址" suggestion appears |  |  |
| Link dialog endpoint matches certificate | No certificate mismatch warning |  |  |
| Source task starts | State becomes "连接中" or waits for target |  |  |
| Target "测试连接" succeeds | Direct result is usable |  |  |
| Target joins link | Target task is created |  |  |
| First sync completes | Target receives source files |  |  |
| Modified source file updates target | Source version wins |  |  |
| Target-only file remains | File is not deleted |  |  |

## Relay checklist

| Check | Expected | Result | Notes |
| --- | --- | --- | --- |
| Relay starts | Logs show Relay listening |  |  |
| Source and target trust Relay certificate | Relay TLS check is usable |  |  |
| Source link includes Relay endpoint | Link test checks Relay |  |  |
| Direct path unavailable or disabled | Direct may fail when expected |  |  |
| Relay fallback succeeds | Target sync completes through Relay |  |  |
| Relay-only source works without `-cert/-key` | Link generation requires Relay field |  |  |

## Negative checks

| Check | Expected | Result | Notes |
| --- | --- | --- | --- |
| Wrong CA on target | "测试连接" fails certificate verification |  |  |
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
