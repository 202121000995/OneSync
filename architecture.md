# OneSync V1 技术架构

## 1. 项目定位

OneSync 是面向个人用户的单向文件同步工具。

V1 数据流固定为：

```text
源目录 -> 文件扫描 -> 差异计算 -> 文件传输 -> 目标目录
```

V1 支持：

- Windows 客户端
- Linux 服务端
- 文件新增、修改、删除
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

## 2. 技术原则

- 后端与客户端核心使用 Go。
- 优先使用 Go 标准库，非必要不引入第三方依赖。
- 同步核心不依赖 Web、托盘或具体网络实现。
- 网络层只负责连接和字节传输，不判断文件同步逻辑。
- 所有跨平台文件路径在协议中使用相对路径和 `/` 分隔符。
- 本地落盘前必须清理并校验路径，禁止写出目标根目录。
- 删除操作必须来自已确认的源端差异，不根据传输失败推断删除。
- V1 先保证正确性和可恢复性，再优化并发与性能。

## 3. 目录结构

```text
/
├── architecture.md
├── go.mod
├── cmd/
│   ├── onesync/
│   └── relay/
├── backend/
├── frontend/
├── internal/
│   ├── auth/
│   ├── config/
│   ├── filewatch/
│   ├── logger/
│   ├── network/
│   ├── relay/
│   ├── scanner/
│   ├── sync/
│   └── task/
└── tests/
```

目录职责：

- `cmd/onesync`：OneSync 主程序入口，负责组装组件。
- `cmd/relay`：Relay 服务入口。
- `backend`：Web API 与静态资源服务，不包含同步算法。
- `frontend`：Web 管理后台。
- `internal/scanner`：目录遍历与文件快照生成。
- `internal/filewatch`：后续增量事件监听；V1 初期可由周期扫描驱动。
- `internal/sync`：快照比较、同步计划与执行编排。
- `internal/network`：TCP 连接、协议帧和传输会话。
- `internal/relay`：中转连接配对和数据转发。
- `internal/task`：同步任务生命周期与状态管理。
- `internal/config`：配置读取、校验和持久化。
- `internal/auth`：同步链接令牌和会话认证，不扩展为用户权限系统。
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
    OperationDelete OperationType = "delete"
)

type Operation struct {
    Type  OperationType
    Entry FileEntry
}
```

删除操作只使用 `Entry.Path`。操作必须按确定性顺序输出，便于测试和恢复。

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
}
```

`Role` 在 V1 只能为 `source` 或 `target`。任务状态至少包含：

```text
created -> connecting -> syncing -> idle
                      \-> failed
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
- 两端存在但内容不同：`update`
- 仅目标端存在：`delete`
- 内容一致：不生成操作

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
- 成功传输后执行删除。
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
- `file_begin`：文件元数据和续传偏移。
- `file_chunk`：文件数据块。
- `file_end`：文件大小与哈希确认。
- `delete`：删除相对路径。
- `ack`：操作确认。
- `error`：结构化错误。
- `ping` / `pong`：连接保活。

协议要求：

- 每个消息必须有最大长度限制。
- 文件分块传输，禁止整文件读入内存。
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
9. 执行删除操作
10. 更新任务状态并进入 idle
11. 文件事件或周期扫描触发下一轮同步
```

同一任务同一时间只允许一个同步周期运行。新的触发信号应合并，避免并发修改同一目标文件。

## 8. Relay 架构

Relay 只提供：

- 源端和目标端连接注册。
- 基于会话 ID 的连接配对。
- 双向字节流转发。
- 空闲超时、流量限制和连接清理。

Relay 不保存文件、不解析文件内容、不生成同步计划。同步令牌不能以明文写入日志。

## 9. Web 管理边界

V1 Web 管理后台提供：

- 创建源端或目标端任务。
- 生成和加入同步链接。
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

## 10. 配置与持久化

V1 不引入数据库。配置与任务状态使用本地文件持久化：

- 写入临时文件。
- 刷新并关闭文件。
- 原子重命名替换正式文件。
- 文件格式必须包含版本号。
- 进程启动时校验配置，不接受未知的关键字段值。

运行时状态和可恢复状态分离；连接对象、上下文和瞬时进度不写入配置文件。

## 11. 安全要求

- 所有远端路径必须经过路径穿越检查。
- 限制协议帧、文件块和任务数量。
- 对令牌使用恒定时间比较。
- 日志脱敏同步令牌、链接和本地敏感路径。
- 默认只绑定配置指定的监听地址。
- 接收文件前检查目标目录、可用空间和文件类型。
- 删除根目录、空路径和 `.` 路径必须被拒绝。
- V1 的 TCP 与 Relay 通信若未实现加密，只允许作为明确记录的限制，不能宣称链路安全。

## 12. 测试策略

单元测试：

- 路径规范化和路径穿越拒绝。
- 文件扫描与忽略规则。
- 新增、修改、删除差异计算。
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

## 13. 开发任务拆分

每项任务使用独立 `feature/xxx` 分支，并依次经过 Core、Tester、Reviewer。

1. `feature/project-bootstrap`
   - 初始化 Go 模块和目录骨架。
   - 建立基础配置、日志接口和构建命令。
2. `feature/file-scanner`
   - 实现文件快照与扫描器。
   - 增加跨平台路径单元测试。
3. `feature/file-differ`
   - 实现新增、修改、删除差异计算。
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

## 14. 待确认项

以下内容在进入对应开发任务前必须由项目负责人确认：

- Go 模块路径。
- 最低 Go 版本。
- 配置文件格式和默认存储位置。
- TCP 与 Relay 是否要求 V1 即支持 TLS。
- 同步链接的有效期和重复使用规则。
- 符号链接的最终处理策略。
- 文件冲突时是否始终以源端覆盖目标端。
- 目标端存在未同步文件时是否允许自动删除。

