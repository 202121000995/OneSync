# OneSync Win7 Qt 客户端

这是 OneSync 的 Windows 7 兼容客户端工程。它独立于当前 Go 主客户端，目标是解决 Windows 7 对现代 TLS、系统证书链、托盘和浏览器能力支持不足的问题。

## 当前阶段

当前骨架已经包含：

- Qt Widgets 主窗口。
- 粘贴同步链接。
- 解析 OneSync 同步链接。
- 选择目标接收目录。
- 显示源端地址、Relay 地址、会话编号和过期时间。
- 导出诊断文本。

尚未接入：

- TLS/Relay 连接。
- 同步身份认证。
- 目录扫描。
- 文件接收和断点续传。
- 托盘和后台常驻。

## 技术选择

- Qt 5.x Widgets。
- CMake 工程。
- Windows 7 目标宏：`_WIN32_WINNT=0x0601`。
- TLS 推荐使用随程序分发的 OpenSSL，不依赖 Windows 7 系统 TLS 能力。

不建议使用 Qt 6 做 Win7 客户端。

## 构建示例

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

Win7 客户端第一版只做目标端接收：

1. 粘贴同步链接。
2. 选择接收目录。
3. 通过 Relay TLS 连接源端。
4. 完成同步认证。
5. 接收源端文件。
6. 同路径冲突时源端覆盖目标端。
7. 目标端多余文件不删除。
8. 导出诊断日志。

源端创建链接、设备管理、复杂参数等能力先由 Win10/Linux 主客户端承担。

## 与主协议的关系

Win7 Qt 客户端必须兼容当前 OneSync 协议：

- 同步链接：Base64URL 编码 JSON，版本 `1`。
- Relay 登记协议：版本 `2`，支持 Relay 访问令牌。
- 网络帧协议：版本 `1`，14 字节帧头。
- 同步认证：目标端发送 peer identity + 同步令牌。
- 文件传输：分块接收、SHA-256 校验、临时文件落盘后替换。

详细字段见 `docs/protocol-notes.md`。
