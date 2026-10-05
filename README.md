# Clash 订阅管理器

一个自托管的 Clash 订阅管理面板：把机场订阅、自建节点统一管理起来，自动转换格式，并按模板生成你自己的 Clash 配置链接。

本 fork 也支持把已有的本地完整 Clash YAML 集中托管：在模板页面选择「完整配置原样托管」，导入 `.yaml` / `.yml` 文件或直接粘贴，保存后复制配置地址到各台客户端。以后在这里修改，各客户端通过更新该订阅获取新内容。原样托管保留节点、分组、规则、DNS、注释与换行，不重新生成配置；只提供完整配置下载。

「订阅生成」模式保留上游的模板组合功能。已有 v1.0.20 模板以及省略 `mode` 的 API 请求仍按订阅生成模式处理；新建页面默认使用原样托管。保存前会验证 YAML，校验失败保留上一份配置。配置文件与缓存通过原子替换发布，并发保存按完整事务串行执行。

程序内嵌网页资源，单个可执行文件可在任意工作目录运行。配置文件仍从当前目录的 `config.yaml` 读取；`listen_address: "127.0.0.1"` 可用于 Nginx 反向代理，空值则监听所有接口。管理密钥保存在配置文件中，不写入启动日志。

Linux systemd 服务模板见 [deploy/clash-subscription-manager.service](deploy/clash-subscription-manager.service)。创建专用 `clash-config` 用户，将程序放在 `/opt/clash-subscription-manager/`，将配置放在 `/etc/clash-subscription-manager/config.yaml`，设置 `data_dir: /var/lib/clash-subscription-manager`、`listen_address: "127.0.0.1"` 和自行选择的空闲 `port`。启动前必须填写随机生成的管理 `token`，不要保留默认占位符；服务用户不能改写由 root 管理的配置。配置文件应设置为 `root:clash-config`、`0640`，目录为 `0750`。通过 Nginx 提供 HTTPS 时，应用的 `https` 设为 `false`，并限制允许的局域网来源。订阅 URL 含访问令牌，应避免在反向代理访问日志中记录查询参数。

适用场景：想在一台服务器/软路由上集中管理多个机场订阅，生成带节点筛选、自选分组、固定规则的个人 Clash 配置，分发给电脑、手机等多个设备使用。

[![Docker Pulls](https://img.shields.io/docker/pulls/zhf883680/clash-subscription-manager)](https://hub.docker.com/r/zhf883680/clash-subscription-manager)

## 功能一览

**订阅管理**
- 支持两种来源：输入「订阅地址」，或直接粘贴 `ss://` / `vmess://` / `trojan://` / `vless://` / `ssr://` 节点文本合并成一条订阅
- 自动识别订阅格式并转换为 Clash YAML，本地缓存生成文件
- 每个订阅可单独设置：节点筛选 Filter、自定义请求头、手动刷新
- 订阅地址每天自动刷新一次（启动时也会立即刷新）

**模板管理**
- 页面里直接编辑 Clash 模板 YAML，可创建多份模板
- 每个模板可绑定全部或部分订阅，下载时自动填充 `proxy-providers`
- 同一份模板提供三种输出，适配不同客户端

**安全**
- 访问需登录（访问密钥）
- 每个订阅/模板的对外链接带独立随机密钥，可随时「重置链接」使旧链接失效

## 快速开始（Docker，推荐）

### 1. 运行

```bash
docker run -d --name clash-manage \
  -p 8080:8080 \
  -e TOKEN="换成你自己的强随机密钥" \
  -v $(pwd)/data:/app/data \
  zhf883680/clash-subscription-manager:latest
```

说明：
- `-e TOKEN=...`：指定访问密钥（登录用），不设置也行，见下方「访问密钥」。
- `-v $(pwd)/data:/app/data`：数据目录挂载到宿主机，**务必挂载**，订阅/模板/缓存文件都在这里，容器重建后数据不丢。

### 2. 打开页面

浏览器访问 `http://你的地址:8080`，输入访问密钥登录后即可使用。

## 访问密钥

| 场景 | 密钥来源 |
| --- | --- |
| 设置了环境变量 `TOKEN` | 直接使用 `TOKEN` 的值（优先，不会改动 config） |
| 配置文件里有非默认 `token` | 使用配置文件里的值 |
| 都没设置（或还是默认值） | 启动时自动生成 32 位随机密钥，写入 `config.yaml` 并打印到日志 |

找不到密钥时：

```bash
docker logs clash-manage | grep "访问密钥"
# 例如输出：未配置访问密钥，已随机生成并写入 config.yaml: 1a2b3c...
```

> 注意：容器重建后，如果没有挂载 `config.yaml` 或设置 `TOKEN`，自动生成的密钥会变。长期使用建议用 `-e TOKEN=...` 固定密钥。

## 使用指南

### 1. 添加订阅

「订阅管理」页面：

- **订阅地址**：填机场给的订阅 URL；如有需要可展开「高级设置」填筛选 Filter、自定义请求头（比如带鉴权的 Header）。
- **节点文本**：直接粘贴一个或多个节点链接，系统会自动合并成一条订阅。

添加后系统会立即下载、转换并缓存配置。

### 2. 订阅列表

每条订阅可以：

- **复制下载地址**：该订阅转换后的完整 Clash YAML 直链，可单独放进 Clash 使用。
- **刷新**：立即重新拉取并更新缓存（URL 来源的订阅）。
- **编辑**：改名称/URL/筛选/请求头。「仅保存修改」不重新下载；「保存并更新」会刷新内容。
- **重置链接**：重新生成该订阅的下载地址，**旧地址立即失效**（详见「对外链接与重置」）。
- **删除**：同时删除记录和缓存文件。

### 3. 使用模板生成配置

「模板管理」页面：左侧模板列表 + 右侧编辑器，直接编辑 YAML，保存后点右上角「复制地址 ▾」选择输出：

| 输出 | 用途 |
| --- | --- |
| 复制模板地址 | proxy-providers 模式，给 Clash / Mihomo 等作为订阅链接 |
| 复制全节点地址 | 把节点展开成完整 `proxies` 配置，适合不走 provider 的场景 |
| 复制非 Clash 模式 | 纯节点链接文本，可导入 Shadowrocket / Loon 等工具 |

提示：
- 模板可绑定全部或部分订阅，并给订阅自定义前缀；绑定的订阅会在渲染时自动填入 `proxy-providers`。
- 订阅若配置了 Filter，渲染出的 provider 会带上对应筛选规则。

### 4. 在 Clash 里使用

以 Clash Verge / Mihomo 为例：订阅管理里「新建」，粘贴上面复制的**模板地址**，类型选 Clash，保存并更新即可。之后服务端每天自动刷新订阅，Clash 也会按自己的更新间隔重新拉取。

### 5. 对外链接与重置（重要）

所有对外链接都自带独立的随机密钥：

```
http://你的地址:8080/download/订阅ID?token=xxxx
http://你的地址:8080/api/templates/模板ID/render?token=xxxx
```

- 链接等于密码：**不要公开分享**。一旦链接泄露，到页面上点对应条目/模板的「重置链接」，旧链接立即失效，再复制一次新链接即可，无需改全局密钥。
- 重置订阅后，用该订阅渲染出的模板配置也会自动带上新的下载密钥，不需要单独处理。
- 从旧版本升级后，之前的链接（不带 `?token=`）会失效，请重新复制一次。

## 安全建议（公网部署）

- 一定使用强随机访问密钥，不要在公网用默认值；见「访问密钥」。
- 服务对外建议走 HTTPS：用 Caddy / Nginx 反代自动签发证书，或配置 `https: true` 并提供 `cert.pem`/`key.pem`。
- 用 Nginx/Caddy 反代时，不要再给页面套一层 basic auth（页面自带登录），否则会重复认证；反代只负责 HTTPS 即可。
- 若不需要公网探活，可在反代层屏蔽 `/health`。

## 配置说明

配置文件 `config.yaml`（也可用环境变量覆盖部分项）：

| 配置 | 默认 | 说明 |
| --- | --- | --- |
| `port` | `8080` | 监听端口 |
| `data_dir` | `./data` | 数据目录（订阅/模板/缓存文件） |
| `token` | 自动生成 | 访问密钥；优先用环境变量 `TOKEN` 覆盖 |
| `rate_limit` | `60` | 每分钟每个 IP 允许的下载请求数 |
| `download_timeout` | `30s` | 拉取订阅的超时时间 |
| `max_file_size` | `52428800` | 单次下载内容大小上限（字节） |
| `https` | `false` | 为 `true` 时使用同目录 `cert.pem` / `key.pem` 开启 HTTPS |

> `backup_enabled`、`backup_interval`、`file_retention_days` 目前为预留项，暂未启用，可忽略。

## 数据与备份

数据都在 `data_dir` 里（默认 `./data`）：

```
data/
├── subscriptions.json   # 订阅记录
├── templates.json       # 模板记录
└── *.yaml               # 各订阅的缓存配置
```

备份整个 data 目录即可；Docker 部署时把该目录挂载出来（见快速开始）。

## 从源码运行（开发/自编译）

需要 Go 1.25+：

```bash
go run .
```

应用从当前目录的 `config.yaml` 读取配置，默认端口 `8080`。

## 常见问题

**忘记密钥了怎么办？**
- 设置了 `TOKEN` 环境变量：重启时换一个即可。
- 配置里有 token：直接改 `config.yaml` 里的 `token` 再重启。
- 自动生成的：`docker logs <容器名> | grep 访问密钥` 找回；容器重建后会重新生成，这时日志里能看到新密钥。

**之前配好的 Clash 订阅突然 404？**
大概率是升级后旧链接失效了，回到页面重新「复制模板地址 / 复制下载地址」更新一次即可。

**想让某条链接彻底失效？**
在对应订阅或模板上点「重置链接」，旧链接立即作废。

**面板很卡 / 被大量扫描？**
公网部署请务必设置强密钥并套 HTTPS；反代层可以做请求频率限制，只放行你需要的路径。

## 相关链接

- GitHub：https://github.com/zhf883680/clash-subscription-manager
- Docker Hub：https://hub.docker.com/r/zhf883680/clash-subscription-manager

## 致谢

转换逻辑参考了 [tindy2013/subconverter](https://github.com/tindy2013/subconverter)。
