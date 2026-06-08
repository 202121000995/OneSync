# OneSync V1 技术架构

## 1. 项目定位

OneSync 是面向个人用户的单向文件同步工具。

V1 数据流固定为：

```text
源目录 -> 文件扫描 -> 差异计算 -> 文件传输 -> 目标目录
```

V1 支持：

- Windows 10 及以上客户端
- Linux 服务端
- 文件新增、修改
- TCP 直连
- Relay 中转
- Web 管理后台

V1 不支持：

- 双向同步
- 多用户与权限系统
- 集群
- UDP 打洞
- QUIC
- WebDAV、S3、对象存储
- macOS 客户端
- Windows 7 客户端；后续作为独立兼容客户端评估，技术栈不限定为 Go

## 2. 技术原则

- 后端与客户端核心使用 Go。
- 优先使用 Go 标准库，非必要不引入第三方依赖。
- 同步核心不依赖 Web、托盘或具体网络实现。
- 网络层只负责连接和字节传输，不判断文件同步逻辑。
- 所有跨平台文件路径在协议中使用相对路径和 `/` 分隔符。
- 本地落盘前必须清理并校验路径，禁止写出目标根目录。
- 目标端独有文件在 V1 保留，不通过同步协议执行删除。
- V1 先保证正确性和可恢复性，再优化并发与性能。

## 3. 目录结构

```text
/
├── architecture.md
├── go.mod
├── cmd/
│   ├── onesync-cert/
│   ├── onesync/
│   └── relay/
├── backend/
│   └── web/
├── packaging/
│   └── systemd/
├── internal/
│   ├── auth/
│   ├── certutil/
│   ├── config/
│   ├── diagnostic/
│   ├── filewatch/
│   ├── logger/
│   ├── network/
│   ├── progress/
│   ├── relay/
│   ├── scanner/
│   ├── sync/
│   ├── transfer/
│   └── task/
└── tests/
```

目录职责：

- `cmd/onesync`：OneSync 主程序入口，负责组装组件。
- `cmd/relay`：Relay 服务入口。
- `cmd/onesync-cert`：本地 TLS 证书生成辅助工具，用于测试或小型私有部署。
- `backend`：Web API 与静态资源服务，不包含同步算法。
- `backend/web`：Web 管理后台静态资源。
- `packaging/systemd`：Linux systemd 服务模板和部署说明。
- `internal/scanner`：目录遍历与文件快照生成。
- `internal/filewatch`：后续增量事件监听；V1 初期可由周期扫描驱动。
- `internal/sync`：快照比较、同步计划与执行编排。
- `internal/transfer`：文件分块发送、临时接收、哈希校验和断点续传。
- `internal/network`：TCP 连接、协议帧和传输会话。
- `internal/progress`：任务级文件进度快照和校验。
- `internal/relay`：中转连接配对和数据转发。
- `internal/task`：同步任务生命周期与状态管理。
- `internal/config`：配置读取、校验和持久化。
- `internal/auth`：同步链接令牌和会话认证，不扩展为用户权限系统。
- `internal/certutil`：生成本地自签 TLS 证书；不承担生产证书签发、吊销或轮换管理。
- `internal/diagnostic`：执行短连接 TLS 诊断，用于检查端口、证书和 CA 配置，不发送同步令牌。
- `internal/logger`：统一日志接口和标准库实现。
- `tests`：跨模块集成测试与回归测试。

## 4. 核心数据结构

### 4.1 文件条目

```go
type FileEntry struct {
    Path    string
    Size    int64
    ModTime int64
    Mode    uint32
    Hash    string
}
```

约束：

- `Path` 是相对同步根目录的规范化路径。
- `ModTime` 使用 Unix 纳秒时间。
- 普通文件参与 V1 同步。
- 符号链接、设备文件和命名管道在 V1 默认忽略。
- OneSync 保留目录 `.onesync-part` 整目录忽略，不参与快照或同步。
- `Hash` 用于确认内容一致性；扫描策略可先比较大小和修改时间，再按需计算。

### 4.2 目录快照

```go
type Snapshot struct {
    RootID      string
    GeneratedAt int64
    Files       map[string]FileEntry
}
```

`RootID` 标识同步源，不暴露源目录绝对路径。

### 4.3 同步操作

```go
type OperationType string

const (
    OperationCreate OperationType = "create"
    OperationUpdate OperationType = "update"
)

type Operation struct {
    Type  OperationType
    Entry FileEntry
}
```

V1 不自动删除目标端独有文件。操作必须按路径确定性排序输出，便于测试和恢复。

### 4.4 同步任务

```go
type Task struct {
    ID          string
    Role        string
    SourcePath  string
    TargetPath  string
    PeerAddress string
    RelayURL    string
    State       string
    Progress    *Progress
}

type Progress struct {
    TotalFiles     int
    CompletedFiles int
    CurrentPath    string
}
```

`Role` 在 V1 只能为 `source` 或 `target`。任务状态至少包含：

```text
created -> connecting -> syncing -> idle
                      \-> failed
                      \-> stopped
```

停止状态由用户操作触发，不与网络错误混用。

## 5. 模块接口

### 5.1 Scanner

```go
type Scanner interface {
    Scan(ctx context.Context, root string) (Snapshot, error)
}
```

职责：

- 遍历根目录。
- 生成规范化相对路径。
- 收集文件元数据。
- 根据策略计算内容哈希。
- 正确处理扫描期间文件消失、权限不足和上下文取消。

### 5.2 Differ

```go
type Differ interface {
    Compare(source Snapshot, target Snapshot) ([]Operation, error)
}
```

规则：

- 仅源端存在：`create`
- 两端存在但内容不同：`update`，始终以源端为准
- 仅目标端存在：保留，不生成操作
- 内容一致：不生成操作

比较双方均有哈希时以哈希为准；任一方缺少哈希时比较大小和修改时间。权限模式不参与跨平台内容差异判断。

### 5.3 Transport

```go
type Transport interface {
    Connect(ctx context.Context, endpoint string) (Session, error)
}

type Session interface {
    Send(ctx context.Context, message Message) error
    Receive(ctx context.Context) (Message, error)
    Close() error
}
```

`Transport` 的首个实现为 TCP。Relay 必须保持相同会话语义，使同步引擎无需区分直连和中转。

### 5.4 Sync Engine

```go
type Engine interface {
    Run(ctx context.Context, taskID string) error
}
```

职责：

- 获取源端和目标端快照。
- 生成同步计划。
- 按协议传输文件。
- 在目标端原子替换完整文件。
- 保留目标端独有文件，不执行自动删除。
- 汇报任务进度和错误。

### 5.5 Task Manager

```go
type TaskManager interface {
    Create(ctx context.Context, task Task) error
    Start(ctx context.Context, taskID string) error
    Stop(ctx context.Context, taskID string) error
    Get(ctx context.Context, taskID string) (Task, error)
    List(ctx context.Context) ([]Task, error)
}
```

任务管理层负责生命周期，不实现扫描、差异或传输算法。

## 6. 传输协议

V1 使用带长度前缀的二进制帧。消息头包含：

```text
version
message_type
request_id
payload_length
```

V1 消息类型：

- `hello`：协议版本与节点信息。
- `authenticate`：同步链接令牌认证。
- `snapshot_request` / `snapshot_response`：交换目录快照。
- `sync_plan`：声明本轮新增和更新操作数量。
- `file_begin`：文件元数据和续传偏移。
- `file_chunk`：文件数据块。
- `file_end`：文件大小与哈希确认。
- `sync_complete`：本轮所有文件完成。
- `ack`：操作确认。
- `error`：结构化错误。
- `ping` / `pong`：连接保活。

协议要求：

- TCP 与 Relay 会话必须使用 TLS 1.3；客户端必须校验证书，禁止跳过验证。
- TLS 证书、私钥和信任根由配置层提供，网络层不自动生成或持久化密钥。
- `onesync-cert` 可生成带 DNS/IP SAN 的本地自签证书；客户端通过 `-ca` 显式信任该证书或 CA bundle，证书验证仍不可关闭。
- 每个消息必须有最大长度限制。
- 文件分块传输，禁止整文件读入内存。
- V1 文件块最大为 256 KiB，接收端必须逐块确认已落盘偏移。
- 临时文件写入完成并校验后，再原子替换目标文件。
- 断点信息至少包含路径、文件标识、已确认偏移和临时文件位置。
- 连接恢复后必须重新认证，并由接收端确认可续传偏移。
- 不允许目标端直接使用对端传来的绝对路径。

## 7. 同步流程

```text
1. 启动任务
2. 建立 TCP 直连；失败时按配置连接 Relay
3. 双方交换协议版本并认证同步令牌
4. 源端生成快照
5. 目标端生成快照
6. 源端计算单向差异
7. 依次执行新增和修改
8. 校验文件大小与哈希
9. 更新任务状态并进入 idle
10. 文件事件或周期扫描触发下一轮同步
```

同一任务同一时间只允许一个同步周期运行。新的触发信号应合并，避免并发修改同一目标文件。
一个引擎实例绑定一个任务、同步根目录和会话，不允许不同任务复用同一实例。

## 8. Relay 架构

Relay 只提供：

- 源端和目标端连接注册。
- 基于会话 ID 的连接配对。
- 双向字节流转发。
- 空闲超时、流量限制和连接清理。

Relay 不保存文件、不解析文件内容、不生成同步计划。同步令牌不能以明文写入日志。

Relay 登记协议包含版本、角色、会话 ID 和 32 字节高熵令牌。只有会话 ID 相同、角色互补且令牌一致的两个连接才会配对。令牌仅在内存中以 SHA-256 摘要参与恒定时间比较，日志只记录会话 ID 的截断摘要。

Relay 服务端必须配置 TLS 证书和私钥，并强制 TLS 1.3。默认资源边界：

- 等待配对超时 2 分钟。
- 已配对字节流空闲超时 5 分钟。
- 最多 1,000 个等待会话。
- 最多 1,000 个活动会话。
- 总连接数上限由等待会话和活动会话上限共同推导。
- 每个会话每个方向最多转发 1 TiB。

上述限制可通过 Relay 启动参数调整，但不能关闭。

## 9. Web 管理边界

V1 Web 管理后台提供：

- 创建源端或目标端任务。
- 生成和加入同步链接。
- 粘贴同步链接后测试直连和 Relay TLS 可达性。
- 启动、停止任务。
- 查看连接状态、同步进度和最近错误。

Web API 仅调用 `TaskManager` 等应用接口，不直接访问扫描器或网络连接。

同步链接应包含：

```text
protocol version
task/session identifier
connection endpoint
relay endpoint（可选）
一次性或高熵同步令牌
```

同步链接不得包含本地绝对路径。
同步链接有效期固定为 24 小时，成功认证后一次性失效。
同步令牌与任务状态分开存储，任务列表和管理 API 不返回明文令牌。
测试连接接口只解析链接中的公开端点并执行 TLS 握手，不发送同步令牌，不创建任务，也不消耗一次性链接。
管理页面默认仅绑定 `127.0.0.1`，并校验请求 Host 与 Origin。
Windows 10 及以上客户端启动后自动打开本机管理页；非 Windows 客户端不自动弹出浏览器。

## 10. 客户端运行器

主程序通过任务运行器把任务管理、私密凭据、TLS/Relay 连接和同步引擎组装起来。

任务启动后默认持续运行。每一轮执行：

```text
读取最新凭据 -> 建立直连或 Relay 连接 -> 同步身份认证 -> 执行单轮同步 -> 关闭连接 -> 等待下一次周期触发
```

V1 的持续触发由 `internal/filewatch` 的周期等待实现，默认间隔 30 秒，可通过 `-sync-interval` 调整。周期扫描不依赖平台文件事件，后续可在同一模块内替换为系统文件监听。

长期运行任务会在每轮之间更新状态：

- `connecting`：正在建立直连或 Relay 连接，并完成同步身份认证。
- `syncing`：正在执行本轮快照、差异和文件传输。
- `idle`：本轮已结束，正在等待下一次周期触发。

同步引擎会在每轮同步中汇报文件级进度：

- `TotalFiles`：本轮需要创建或更新的文件数。
- `CompletedFiles`：本轮已完成的文件数。
- `CurrentPath`：源端当前正在发送的相对路径；目标端无法在不解析传输层细节时提前知道文件名，可只汇报数量。

管理页按周期刷新任务列表，显示本轮 `CompletedFiles/TotalFiles` 和源端当前文件。

源端任务：

- 从独立凭据文件读取会话 ID、连接端点、可选 Relay 端点和同步令牌。
- 有 TLS 证书时在链接端点端口监听直连。
- 有 Relay 端点时同时候选 Relay 连接。
- 只有完成 TLS、Relay 配对和同步身份认证后的连接才会进入同步引擎。
- 首次认证成功时把目标端设备身份绑定到源端凭据；后续只允许同一目标设备重连。
- 每一轮同步前重新读取凭据，确保首次绑定目标设备后下一轮立即使用最新身份约束。

目标端任务：

- 加入同步链接时生成本机稳定设备身份，并与私密凭据一起保存。
- 启动时先尝试直连源端 TLS 地址。
- 直连连接或认证失败后，如配置了 Relay，则尝试 Relay。
- 成功认证后运行目标端同步引擎。

连接暂时不可用时，运行器等待下一次周期触发后重试；同步认证失败、凭据无效、路径错误或同步协议错误会让任务进入失败状态。

客户端 TLS 配置：

- `-cert` 与 `-key` 提供源端直连监听证书和私钥。
- `-ca` 可追加自建 CA；未提供时使用操作系统信任库。
- 本地验收可使用 `onesync-cert` 生成源端或 Relay 证书；若直连证书和 Relay 证书不同，可把多个 PEM 证书合并到同一个 `-ca` 文件。
- 管理页测试连接使用同一套客户端 TLS 配置和 `-ca` 信任根。
- 禁止关闭服务端证书验证。

## 11. Linux 服务

Linux 端通过 systemd 作为长期运行服务管理。

主程序和 Relay 均支持：

- 前台运行，由 systemd 负责拉起、停止和失败重启。
- 收到 `SIGTERM` 后走统一上下文取消路径，停止监听并等待正在处理的连接退出。
- 默认日志输出到标准输出，由 journald 收集。
- 可选 `-log-file` 参数，把日志追加写入本地文件，文件权限为 `0600`。

`packaging/systemd` 提供：

- `onesync.service`：运行管理页和同步任务。
- `onesync-relay.service`：运行 Relay 中转服务。
- `systemd.md`：Linux 安装、启动、停止和查看日志说明。

systemd 模板使用专门的 `onesync` 用户、`StateDirectory=onesync` 和 `LogsDirectory=onesync`。模板不默认启用强文件系统沙箱，因为同步源目录和目标目录可能位于 `/home`、外接盘或自定义挂载点；部署者可在明确同步目录后再增加更严格的 `ReadWritePaths`。

## 12. 配置与持久化

V1 不引入数据库。配置与任务状态使用本地文件持久化：

- 任务状态文件路径由调用方提供；平台默认位置在客户端集成阶段确定。
- OneSync 主程序默认使用操作系统标准用户配置目录下的 `OneSync` 目录。
- 文件使用带版本号的严格 JSON 格式。
- 写入临时文件。
- 刷新并关闭文件。
- 原子重命名替换正式文件。
- 文件格式必须包含版本号。
- 进程启动时校验配置，不接受未知的关键字段值。

运行时状态和可恢复状态分离；连接对象、上下文和瞬时进度不写入配置文件。

## 13. 安全要求

- 所有远端路径必须经过路径穿越检查。
- 限制协议帧、文件块和任务数量。
- 对令牌使用恒定时间比较。
- 日志脱敏同步令牌、链接和本地敏感路径。
- 默认只绑定配置指定的监听地址。
- 接收文件前检查目标目录、可用空间和文件类型。
- 删除根目录、空路径和 `.` 路径必须被拒绝。
- TCP 与 Relay 通信强制使用 TLS 1.3，客户端必须验证服务端证书。
- 一次性同步链接首次成功认证后绑定目标设备身份，复制到其他设备的旧链接不能通过后续认证。

## 14. 测试策略

单元测试：

- 路径规范化和路径穿越拒绝。
- 文件扫描与忽略规则。
- 新增、修改差异计算。
- 协议编码、解码和长度限制。
- 任务状态转换。

集成测试：

- 临时目录之间完成新增、修改、删除同步。
- TCP 连接断开后恢复。
- 大文件分块传输且内存占用有界。
- 断点续传结果与源文件哈希一致。
- Relay 模式与直连模式行为一致。

跨平台回归测试：

- Windows 与 Linux 路径分隔符。
- 大小写敏感差异。
- 长路径和非法文件名处理。
- 文件被占用、权限不足和磁盘空间不足。

## 15. 开发任务拆分

每项任务使用独立 `feature/xxx` 分支，并依次经过 Core、Tester、Reviewer。

1. `feature/project-bootstrap`
   - 初始化 Go 模块和目录骨架。
   - 建立基础配置、日志接口和构建命令。
2. `feature/file-scanner`
   - 实现文件快照与扫描器。
   - 增加跨平台路径单元测试。
3. `feature/file-differ`
   - 实现新增、修改差异计算。
   - 增加确定性排序测试。
4. `feature/tcp-protocol`
   - 实现协议帧、TCP 会话和认证握手。
   - 增加畸形消息与长度限制测试。
5. `feature/file-transfer`
   - 实现分块传输、临时文件、哈希校验和断点续传。
   - 增加大文件与断线集成测试。
6. `feature/sync-engine`
   - 编排快照、差异与传输。
   - 增加端到端单向同步测试。
7. `feature/task-manager`
   - 实现任务生命周期与本地持久化。
   - 增加状态恢复测试。
8. `feature/web-management`
   - 实现管理 API 和 Web 页面。
   - 增加 API 与交互测试。
9. `feature/relay`
   - 实现会话配对和字节流中转。
   - 增加 Relay 模式端到端测试。
10. `feature/windows-client`
    - 集成 Windows 启动与托盘能力。
11. `feature/linux-service`
    - 集成 Linux 服务启动、停止和日志。
12. `feature/acceptance-kit`
    - 增加本地 TLS 证书生成工具和两机验收说明。
13. `feature/connection-check`
    - 增加同步链接 TLS 连接测试 API 和管理页按钮。
14. `feature/sync-progress`
    - 增加任务级文件同步进度持久化和管理页展示。

## 16. 待确认项

以下内容在进入对应开发任务前必须由项目负责人确认：

- Go 模块路径。
- 最低 Go 版本。
- 配置文件格式和默认存储位置。
- 同步链接的有效期和重复使用规则。
- 符号链接的最终处理策略。
