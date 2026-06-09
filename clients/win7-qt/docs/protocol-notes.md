# Win7 Qt 客户端协议接入 notes

本文记录 Qt 客户端需要实现的 OneSync 协议要点。字段以现有 Go 主线为准。

## 1. 同步链接

同步链接是 Base64URL 无填充编码的 JSON。

字段：

```json
{
  "version": 1,
  "session_id": "string",
  "endpoint": "host:port",
  "relay_endpoint": "host:port",
  "relay_token": "string",
  "ca_certificate_pem": "-----BEGIN CERTIFICATE-----...",
  "token": "base64url-32-bytes",
  "issued_at": "RFC3339",
  "expires_at": "RFC3339"
}
```

目标端必须：

- 校验 `version == 1`。
- 校验链接未过期。
- 保存 `session_id`、源端地址、Relay 地址、Relay 令牌、CA 证书和同步令牌。
- 生成本机稳定 `peer_id`，后续源端会绑定该目标端身份。

## 2. Relay 登记协议

Relay TCP/TLS 连接建立后，客户端发送登记数据：

```text
version:       uint8   固定 2
role:          uint8   source=1, target=2
session_len:   uint16  big endian
access_len:    uint16  big endian
session_id:    bytes
sync_token:    32 bytes raw token
access_token:  bytes，可为空
```

目标端 role 固定为 `2`。

Relay 配对成功后返回 1 字节：

```text
0x01
```

## 3. 网络帧

Relay 配对或直连完成后，进入 OneSync 网络帧协议。

帧头 14 字节：

```text
protocol_version: uint8   固定 1
message_type:     uint8
request_id:       uint64  big endian
payload_len:      uint32  big endian
payload:          bytes
```

消息类型：

```text
1  Hello
2  Authenticate
3  SnapshotRequest
4  SnapshotResponse
5  SyncPlan
6  FileBegin
7  FileChunk
8  FileEnd
9  SyncComplete
10 Ack
11 Error
12 Ping
13 Pong
```

## 4. 目标端认证

目标端发送 `Authenticate`，payload：

```text
identity_version: uint8   固定 1
peer_len:         uint16  big endian
peer_id:          bytes
sync_token:       bytes
```

服务端返回同 request id 的 `Ack` 表示认证通过。

## 5. 目标端同步流程

目标端流程：

0. 通过直连源端或 Relay TLS 建立连接。
0. 如果使用 Relay，发送 Relay v2 登记数据并等待 `0x01` 配对成功。
0. 发送带稳定 `peer_id` 的 `Authenticate` 帧。
1. 等待 `SnapshotRequest`。
2. 扫描本地目标目录。
3. 返回 `SnapshotResponse`，payload 为 JSON snapshot。
4. 接收 `SyncPlan`。
5. 返回 `Ack`。
6. 按计划接收多个文件。
7. 接收 `SyncComplete`。
8. 返回 `Ack`。

## 6. 文件接收

文件消息：

- `FileBegin`：包含相对路径、文件大小、SHA-256、file id。
- `FileChunk`：包含 offset 和数据块。
- `FileEnd`：包含最终大小和 SHA-256。

目标端必须：

- 校验相对路径，禁止写出同步目录。
- 写入 `.onesync-part` 临时目录。
- 校验大小和 SHA-256。
- 校验通过后替换目标文件。
- 目标端多余文件不删除。

## 7. TLS

Win7 客户端不要依赖系统 TLS 能力，建议：

- 使用 Qt 5 + OpenSSL。
- 随程序分发 OpenSSL DLL。
- 使用同步链接里的 PEM 证书作为信任锚。
- 不提供“跳过证书验证”开关。

当前主线强制 TLS 1.3。Win7 客户端如需先做兼容验证，可以优先接 Relay TLS，并在服务端兼容策略确认后再决定是否允许兼容 TLS 1.2。

## 8. 当前 Qt 接入进度

已实现：

- 同步链接解析。
- 目标端稳定 `peer_id` 生成和保存。
- 直连/Relay 地址解析。
- TLS socket 建连。
- Relay v2 target 登记。
- `Authenticate` 帧发送和 `Ack` 校验。
- 接收 `SnapshotRequest`。
- 扫描目标目录并生成 OneSync snapshot JSON。
- 发送 `SnapshotResponse`。
- 接收 `SyncPlan`，空同步时确认完成。

下一步：

- 接收 `FileBegin`、`FileChunk`、`FileEnd`。
- 写入 `.onesync-part` 临时目录。
- 校验文件大小和哈希后替换目标文件。
