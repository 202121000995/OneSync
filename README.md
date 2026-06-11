# OneSync

OneSync 是一个源端到目标端的文件同步工具。当前版本用于验收测试：源端负责发送文件，目标端负责接收文件；同路径文件冲突时以源端为准覆盖，目标端多余文件不会自动删除。

## 主要功能

- Windows / Linux 客户端。
- 源端到目标端单向同步。
- 直连同步和 Relay TLS 中转同步。
- 同步链接携带源端证书、Relay 证书和 Relay 令牌，目标端只需要粘贴链接。
- 管理页提供同步任务、设备管理、连接管理、参数、日志、诊断包下载。
- Linux 客户端和 Relay 支持安装、启动、停止、重启、状态、日志、升级、卸载命令。

## 下载

到 GitHub Releases 下载最新验收包：

- Windows：`onesync-windows-amd64-v1.07.zip`
- Linux：`onesync-linux-amd64-v1.07.tar.gz`

发布版本从 `v1.00` 开始递增，当前版本为 `v1.07`，后续版本按 `v1.08`、`v1.09` 继续发布。

## Windows 7 兼容版

当前主 Windows 包面向 Windows 10 及以上系统。Windows 7 兼容版已开始独立开发，采用 Qt 5 Widgets，工程位于：

```text
clients/win7-qt
```

Win7 兼容版当前已支持创建发送任务、加入接收任务、Relay TLS、任务参数、设备管理、连接管理、日志和诊断导出。它是独立 Qt 5 客户端，优先服务 Windows 7/2008 R2 等老系统测试。

## Windows 使用

解压 Windows zip 后，双击：

```text
OneSync.exe
```

OneSync 会打开本机管理页：

```text
http://127.0.0.1:8765
```

Windows 包里的辅助脚本：

- `Open-OneSync.cmd`：重新打开管理页。
- `Open-Logs-Folder.cmd`：打开默认日志目录。
- `Collect-Diagnostics.cmd`：下载诊断包，排查问题时使用。

## Linux 客户端

### 一键安装

普通服务器：

```sh
curl -fsSL https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-onesync.sh | sudo sh
```

中国大陆服务器如果无法直接连接 GitHub，可以使用代理：

```sh
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-onesync.sh | sudo env RELEASE_TAG=v1.07 GH_PROXY=https://gh-proxy.org/ sh
```

如果 GitHub API 或 raw 缓存不可用，可以直接指定 Linux 包地址：

```sh
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-onesync.sh | sudo env PACKAGE_URL=https://gh-proxy.org/https://github.com/202121000995/OneSync/releases/download/v1.07/onesync-linux-amd64-v1.07.tar.gz sh
```

### 手动安装

```sh
tar -xzf onesync-linux-amd64-v1.07.tar.gz
cd onesync-linux-amd64-v1.07
sudo ./onesyncctl install
sudo onesyncctl start
```

安装后管理页默认监听：

```text
http://服务器IP:8765
```

首次打开需要设置管理账号和密码。

### 常用命令

也可以直接输入：

```sh
onesync
```

显示中文菜单。

常用控制命令：

```sh
sudo onesyncctl status
sudo onesyncctl logs
sudo onesyncctl start
sudo onesyncctl stop
sudo onesyncctl restart
sudo onesyncctl upgrade
sudo onesyncctl uninstall
```

### 升级

自动查找最新 Release：

```sh
sudo onesyncctl upgrade
```

固定升级到某个版本：

```sh
sudo env RELEASE_TAG=v1.07 GH_PROXY=https://gh-proxy.org onesyncctl upgrade
```

直接指定 Linux 包地址：

```sh
sudo env PACKAGE_URL=https://gh-proxy.org/https://github.com/202121000995/OneSync/releases/download/v1.07/onesync-linux-amd64-v1.07.tar.gz onesyncctl upgrade
```

### 卸载

```sh
sudo onesyncctl uninstall
```

卸载会移除服务和命令入口，默认保留数据目录和日志，便于排查或重新安装。

## Relay TLS 服务器

Relay 用于源端和目标端无法直连时中转同步连接。Relay 不保存文件内容，只配对并转发加密连接。

### 一键安装

普通服务器：

```sh
curl -fsSL https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=<你的Relay域名或IP> RELAY_PORT=7443 RELAY_TOKEN=<自定义Relay令牌> sh
```

中国大陆服务器可使用代理：

```sh
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=<你的Relay域名或IP> RELAY_PORT=7443 RELAY_TOKEN=<自定义Relay令牌> RELEASE_TAG=v1.07 GH_PROXY=https://gh-proxy.org/ sh
```

如果 GitHub API 或 raw 缓存不可用，可以直接指定 Linux 包地址：

```sh
curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/202121000995/OneSync/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=<你的Relay域名或IP> RELAY_PORT=7443 RELAY_TOKEN=<自定义Relay令牌> PACKAGE_URL=https://gh-proxy.org/https://github.com/202121000995/OneSync/releases/download/v1.07/onesync-linux-amd64-v1.07.tar.gz sh
```

`RELAY_HOSTS` 必须是用户实际会填写到同步链接里的 Relay 域名或 IP，不包含端口。  
`RELAY_PORT` 是 Relay 监听端口。  
`RELAY_TOKEN` 是 Relay 使用令牌，建议使用足够长、不可猜测的字符串。

### 手动安装

```sh
tar -xzf onesync-linux-amd64-v1.07.tar.gz
cd onesync-linux-amd64-v1.07
sudo RELAY_HOSTS=<你的Relay域名或IP> RELAY_PORT=7443 RELAY_TOKEN=<自定义Relay令牌> ./onesync-relayctl install
sudo onesync-relayctl start
```

### 常用命令

也可以直接输入：

```sh
onesyncr
```

显示中文 Relay 菜单。

常用控制命令：

```sh
sudo onesync-relayctl status
sudo onesync-relayctl logs
sudo onesync-relayctl info
sudo onesync-relayctl token
sudo onesync-relayctl rotate-token
sudo onesync-relayctl cert
sudo onesync-relayctl start
sudo onesync-relayctl stop
sudo onesync-relayctl restart
sudo onesync-relayctl upgrade
sudo onesync-relayctl uninstall
```

`sudo onesync-relayctl info` 会一次性输出创建同步需要填写的 Relay 地址、Relay 令牌和 Relay 证书文本。
`sudo onesync-relayctl rotate-token` 会生成新的 Relay 令牌并重启 Relay，旧同步链接会失效，需要重新生成链接。
如果 Relay 域名或 IP 改了，需要重新生成匹配新地址的证书：

```sh
sudo RELAY_HOSTS=<你的Relay域名或IP> RELAY_PORT=7443 onesync-relayctl regen-cert
```

### 升级

自动查找最新 Release：

```sh
sudo onesync-relayctl upgrade
```

固定升级到某个版本：

```sh
sudo env RELEASE_TAG=v1.07 GH_PROXY=https://gh-proxy.org onesync-relayctl upgrade
```

直接指定 Linux 包地址：

```sh
sudo env PACKAGE_URL=https://gh-proxy.org/https://github.com/202121000995/OneSync/releases/download/v1.07/onesync-linux-amd64-v1.07.tar.gz onesync-relayctl upgrade
```

### 卸载

```sh
sudo onesync-relayctl uninstall
```

卸载会移除 Relay 服务和命令入口，默认保留 `/etc/onesync` 下的证书、令牌以及 `/var/log/onesync` 下的日志。

## Relay 在同步链接里怎么填写

在源端生成同步链接时填写：

```text
Relay TLS 地址: <你的Relay域名或IP>:7443
Relay 令牌: <自定义Relay令牌>
```

生成出来的同步链接会携带 Relay 地址、Relay 令牌、源端公开证书和 Relay 公开证书。目标端只需要粘贴同步链接，不需要单独上传证书文件。

## 注意事项

- 管理页端口默认是 `8765`。
- 同步端口默认是 `7443`。
- Relay 端口可以自定义。
- Linux 客户端服务默认绑定 `0.0.0.0:8765`，首次访问会要求设置管理账号密码。
- 如果管理页通过宝塔/Nginx 反代，建议外层使用 HTTPS，并设置强密码。
- 当前验收版仍在快速迭代，正式生产使用前需要完成更多真实多机器测试。
