# OneSync Win7 Qt 客户端

这是 OneSync 的 Windows 7 兼容客户端工程。它独立于当前 Go 主客户端，目标是解决 Windows 7 对现代 TLS、系统证书链、托盘和浏览器能力支持不足的问题。

## 当前阶段

当前骨架已经包含：

- Qt Widgets 主窗口。
- 现代浅色皮肤，左侧导航 + 同步任务表格作为主界面。
- 支持保存多个发送/接收任务。
- “创建同步”弹窗中选择发送目录、填写 Relay 地址并生成同步链接。
- “加入同步”弹窗中粘贴同步链接并选择目标接收目录。
- 选中任务后可以开始、暂停、重新扫描、打开参数、删除。
- 解析 OneSync 同步链接。
- 选择目标接收目录。
- 表格显示任务类型、名称、状态、同步设备、本地大小、全局大小、接收和发送。
- 参数窗口可保存忽略规则，扫描和接收文件时会按规则跳过。
- 运行中任务支持请求暂停，网络读写会在安全点退出。
- 接收/发送列显示任务本轮平均速度，悬停可看收发总量。
- 日志支持全部日志、选中任务和指定任务过滤。
- 可导出全部诊断，也可只导出选中任务诊断。
- 设备管理页可重命名设备、禁用/启用任务设备状态。
- 连接管理页可对选中任务发起 TLS 连通性测试，失败原因会写入任务日志。
- 发送任务可通过 Relay 等待目标端接入，认证后请求目标端快照、生成同步计划并发送新增/变更文件。
- 生成并保存目标端稳定设备身份 `peer_id`。
- 通过 TLS 连接直连源端或 Relay。
- 按 OneSync Relay v2 协议登记目标端。
- 向源端发送带 `peer_id` 的同步认证帧。
- 等待源端 `SnapshotRequest`，扫描目标目录并上报快照。
- 接收源端同步计划；空同步时可确认完成。
- 接收 `FileBegin`、`FileChunk`、`FileEnd`。
- 写入 `.onesync-part` 临时文件。
- 校验 SHA-256 和文件大小后覆盖目标文件。
- 最小化到托盘，托盘菜单支持显示窗口和退出。
- Windows qmake 打包时使用 OneSync 图标。
- 导出诊断文本。

注意：当前开发机已经准备了 Windows Qt 5.12.12 MinGW 32-bit 交叉编译工具链，可直接用 `build-win7.sh` 生成 Win7 x86 测试包。普通 macOS Qt 环境本身仍不能直接生成 Windows `.exe`。

尚未接入：

- 暂停不是杀进程式立即中断；TLS 连接和网络等待最多需要等当前等待点返回。
- 忽略规则已支持常见通配符和目录规则，但还没有规则测试器。
- 收发速度按本轮平均速度显示，后续可改成滑动窗口实时速度。
- 断点续传真实场景校准。
- Win7 实机托盘、现代皮肤和源端 Relay 发送行为验收。

## 技术选择

- Qt 5.x Widgets。
- CMake 工程。
- Windows 7 目标宏：`_WIN32_WINNT=0x0601`。
- TLS 推荐使用随程序分发的 OpenSSL，不依赖 Windows 7 系统 TLS 能力。

不建议使用 Qt 6 做 Win7 客户端。

## 构建示例

### Windows 生成 exe

在 Windows 7/10/11 上安装 Qt 5.12/5.15 后，打开 Qt 对应的命令行环境，例如：

- `Qt 5.12.12 for Desktop (MSVC)`
- `Qt 5.12.12 for Desktop (MinGW)`

建议把工程放在纯英文路径下，例如 `C:\onesync\OneSync`。Qt 5 的 qmake 对中文路径兼容不好。

然后进入本目录运行：

```bat
package-win7.cmd
```

成功后会生成：

```text
clients\win7-qt\dist\OneSyncWin7-win7-qt-v1.07\OneSyncWin7.exe
clients\win7-qt\dist\OneSyncWin7-win7-qt-v1.07.zip
```

这个目录和 zip 会同时包含 Qt DLL。脚本会尽量自动复制 OpenSSL DLL；如果 Win7 上 TLS 连接失败，请检查 `libssl` / `libcrypto` DLL 是否在 exe 同目录。

### 在当前 Mac 上生成 Win7 x86 测试包

当前开发机可复用“轻量化定时备份”项目中的 Zig 和 Windows Qt 5.12.12 MinGW 32-bit 工具链：

```sh
sh clients/win7-qt/build-win7.sh
```

成功后会生成：

```text
clients/win7-qt/release-win7/OneSyncWin7.exe
clients/win7-qt/dist/OneSyncWin7-win7-x86-v1.07.zip
```

这是 32 位 Windows GUI 程序，包内包含 Qt 5 DLL、OpenSSL DLL 和 `platforms/qwindows.dll`，可用于 Windows 7 SP1 测试。

打包脚本会显式使用 `_WIN32_WINNT=0x0601` / `WINVER=0x0601`，并在链接阶段避开 Zig 自带 libc++，防止生成的 EXE 直接导入 Windows 8 才有的 `GetSystemTimePreciseAsFileTime`。

### 使用 qmake

如果本机没有 CMake，但安装了 Qt 5，可以直接使用 qmake：

```bat
qmake OneSyncWin7.pro
nmake
```

macOS 上用于源码编译验证：

```sh
/Users/apple/Qt5.12.12/5.12.12/clang_64/bin/qmake OneSyncWin7.pro
make
```

如果工程路径包含中文，Qt 5.12 的 qmake 可能把路径转成 `????`，导致编译器找不到源码。打包脚本应先把本工程复制到英文临时目录再构建。

### 使用 CMake

在安装 Qt 5 和 CMake 后：

```bat
mkdir build
cd build
cmake .. -G "Visual Studio 16 2019" -A Win32
cmake --build . --config Release
```

64 位构建：

```bat
mkdir build64
cd build64
cmake .. -G "Visual Studio 16 2019" -A x64
cmake --build . --config Release
```

## 最小产品目标

Win7 客户端第一版以 Relay 为主，支持发送和接收：

1. 主界面以同步任务表格为核心。
2. 支持多个发送/接收任务。
3. 创建同步：选择发送目录，填写 Relay 地址和可选 Relay 令牌，生成一段同步链接。
4. 加入同步：粘贴同步链接并选择接收目录。
5. 发送端通过 Relay 等待目标端接入。
6. 接收端通过直连或 Relay TLS 连接源端。
7. 完成同步认证。
8. 源端请求目标端快照，目标端扫描目录并上报快照。
9. 源端扫描发送目录，生成同步计划。
10. 接收端接收同步计划。
11. 源端发送新增/变更文件，接收端校验 SHA-256 和文件大小后覆盖目标文件。
12. 同路径冲突时源端覆盖目标端。
13. 目标端多余文件不删除。
14. 支持忽略规则。
15. 支持暂停当前同步。
16. 显示收发速度。
17. 按任务查看和导出诊断日志。

Win7 源端当前优先支持 Relay 创建同步；直连监听源端后续再做，避免老系统证书和防火墙配置过重。

## 与主协议的关系

Win7 Qt 客户端必须兼容当前 OneSync 协议：

- 同步链接：Base64URL 编码 JSON，版本 `1`。
- Relay 登记协议：版本 `2`，支持 Relay 访问令牌。
- 网络帧协议：版本 `1`，14 字节帧头。
- 同步认证：目标端发送 peer identity + 同步令牌。
- 文件传输：分块接收、SHA-256 校验、临时文件落盘后替换。

详细字段见 `docs/protocol-notes.md`。
