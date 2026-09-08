# Clash 订阅管理器

一个用于管理 Clash 订阅链接、原始节点文本和缓存配置文件的小型 Web 应用，支持自动识别订阅格式并转换为 Clash YAML。

## 页面截图

![订阅管理页面](img/截屏2026-04-12%2013.11.54.png)

![模板管理页面](img/截屏2026-04-12%2013.13.04.png)

![模板编辑页面](img/截屏2026-04-12%2013.13.17.png)

## 功能特性

- 支持两种新增订阅方式：通过 URL 下载，或直接粘贴一个/多个原始节点链接并合并为一条订阅
- 支持直接导入 Clash 订阅，以及自动转换 SS、SSR、VMess、Trojan、VLESS 链接
- 在本地保存转换后的配置文件
- 仅修改订阅 URL、节点筛选 Filter、请求头等信息，不刷新缓存文件
- 按需手动更新订阅并刷新缓存文件
- 通过 Web 界面复制本地下载链接
- 管理多份 Clash 模板
- 在页面中直接编辑模板 YAML
- 模板可选择使用哪些订阅，默认全选
- 下载渲染后的模板配置时，自动用该模板选中的订阅填充 `proxy-providers`
- 支持为每个订阅单独设置可选的 `filter`，并在渲染 `proxy-providers` 时写入
- 同时删除订阅和缓存文件

## 本地运行

```bash
go run .
```

应用从 `config.yaml` 读取配置，默认监听端口 `8080`。
启动时会输出当前版本，例如：

```text
starting clash-subscription-manager v1.0.13 on port 8080
```

## 模板说明

- 页面默认先显示“模板管理”，模板页布局为左侧编辑器、右侧模板列表；订阅页布局为左侧订阅列表、右侧新增订阅。
- 模板支持多份创建、编辑、删除。
- 订阅编辑支持”仅保存修改”和”保存并更新”两种方式。
- 页面里编辑的是基础 YAML；下载模板时，系统会自动根据该模板勾选的订阅生成 `proxy-providers`。
- 新建模板默认会选中全部订阅，也可以按模板单独缩小使用范围。
- 如果订阅填写了 `filter`，渲染后的对应 provider 会带上该字段；未填写时不会输出。
- `复制全节点地址` 会输出展开后的 `proxies` 配置，适用于 Shadowrocket、Loon 等软件。
- 模板下载地址：`/api/templates/{id}/render`

## 订阅说明

- 新增订阅支持 `订阅地址` 和 `节点文本` 两种来源。
- 选择 `节点文本` 时，可以直接粘贴多行节点链接，例如 `ss://...`、`vmess://...`、`trojan://...`、`vless://...`、`ssr://...`。
- 多个节点会在一次提交中合并生成一条订阅记录。
- 节点文本模式会自动忽略空行和无法识别的无效行；如果全部内容都无效，会返回错误。
- `自定义请求头` 仅在 `订阅地址` 模式下生效；节点文本模式不需要请求头。

## Docker 使用

构建镜像：

```bash
docker build -t zhf883680/clash-subscription-manager:latest .
```

运行容器（推荐通过环境变量预配置访问密钥）：

```bash
docker run -d --name clashManage -p 8080:8080 \
  -e TOKEN="请改成你的强随机访问密钥" \
  -v $(pwd)/data:/app/data \
  zhf883680/clash-subscription-manager:latest
```

> **访问密钥（token）规则**
> - 设置了环境变量 `TOKEN`：直接使用它作为访问密钥，不会改动 `config.yaml`。
> - `config.yaml` 中已配置非默认 `token`：直接使用该值。
> - 两者都没有（或还是默认的 `your-secret-token`）：启动时**自动生成随机密钥**，写入 `config.yaml` 并在日志中打印一行，例如：
>   `未配置访问密钥，已随机生成并写入 config.yaml: <32位hex>`
> - 查看日志取密钥：`docker logs clashManage`。注意容器重建后若未挂载配置，生成的密钥会丢失，建议用 `-e TOKEN=...` 或挂载 `config.yaml`。

## 安全说明（公网部署）

- **访问密钥**：`config.yaml` 中的 `token`（或环境变量 `TOKEN`）就是管理密钥。打开页面后需要先输入该密钥才能使用；所有 `/api/*` 管理接口也会校验 `Authorization: Bearer <token>`。未配置时会自动生成并写入 `config.yaml` 且打印到日志，见上文「Docker 使用」。
- **订阅/模板链接**：`/download/{id}` 与 `/api/templates/{id}/render*` 是给 Clash 等客户端匿名拉取的，每个订阅和模板都带独立的随机 `?token=`（旧数据首次启动会自动补齐）。页面上的「重置链接」按钮可以随时作废旧链接——泄露后点一下重置，旧链接立即失效，无需改全局密钥。
- **升级提醒**：本次改动后订阅/模板的对外链接格式变为 `...?token=xxx`，需要重新点「复制下载地址 / 复制地址」获取一次新链接并更新到 Clash。
- **Nginx 反代**：建议把之前对 `/` 的 basic auth 去掉（页面已内置登录），反代只负责 HTTPS 即可；Clash 拉取 `/download/...`、`/api/templates/.../render*` 时链接自带 token，无需额外登录。`/health` 若不需要公网探活可在反代层屏蔽。

## 可优化模块

当前项目在功能层面已经比较完整，下面这些更偏向“后续可维护性优化”，不是必须立即修改的问题。

### 1. 数据存储层

涉及文件：

- `handlers/data.go`
- `handlers/template_store.go`

可优化方向：

- 目前订阅和模板都使用 JSON 文件持久化，结构简单、易于部署，但读改写流程有重复代码。
- 如果后续继续保留自动刷新功能，可以把订阅和模板的存储逻辑进一步收敛成统一的 store 抽象，减少重复实现。
- 如果以后订阅数量明显增加，可以再考虑补充更明确的串行写策略、备份机制或数据库后端；当前单机小规模使用场景下不是刚需。

### 2. 自动刷新模块

涉及文件：

- `handlers/auto_refresh.go`
- `handlers/subscription_refresh.go`

可优化方向：

- 现在自动刷新已经可用，但刷新策略还是偏固定，后续可以把刷新间隔、并发数、失败重试策略做得更清晰。
- 刷新失败时目前主要依赖 `last_error` 和 `last_error_time` 反映状态，后续可以把“最近一次刷新成功 / 失败”的状态语义进一步明确。
- 如果项目长期以单容器、个人使用为主，也可以考虑把自动刷新保持在更保守的配置，优先换取稳定性和可预测性。

### 3. 配置模块

涉及文件：

- `main.go`
- `config.yaml`

可优化方向：

- 当前配置项已经比项目实际使用范围更完整，适合后续扩展。
- 部分配置项更像预留能力，例如 `token`、`backup_enabled`、`backup_interval`、`file_retention_days`，后续可以选择两种方向：
  - 实现对应功能
  - 或删除未使用配置，避免 README 和配置文件给人“已生效”的误解
- 如果后续会频繁部署到不同环境，也可以补充环境变量覆盖配置的能力。

### 4. Handler 组织结构

涉及文件：

- `handlers/handlers.go`
- `handlers/template_handlers.go`

可优化方向：

- 当前 handler 文件已经承担了较多职责，包括请求解析、业务编排、文件操作、渲染输出等。
- 现阶段代码仍然可维护，但如果后续继续加功能，建议逐步拆分为更清晰的 handler / service / store / render 结构。
- 这样做的收益主要不是“代码更高级”，而是后续新增功能时更容易定位修改点，也更容易补测试。

### 5. 模板渲染模块

涉及文件：

- `handlers/template_handlers.go`

可优化方向：

- 当前模板渲染已经支持 `proxy-providers` 和展开后的 `proxies` 两种输出模式，功能上够用。
- 目前部分 YAML 处理仍然是基于字符串替换和局部结构化解析的混合方式，后续如果模板语法支持继续变复杂，可以考虑进一步统一到 YAML AST 处理。
- 这样能减少边缘模板格式下的兼容成本，让模板渲染逻辑更稳定。

### 6. 前端页面模块

涉及文件：

- `templates/index.html`
- `static/js/main.js`
- `static/css/style.css`

可优化方向：

- 目前前端以轻量原生实现为主，部署简单，适合当前项目体量。
- 如果后续交互继续增加，可以考虑把页面状态管理、接口调用、模板编辑相关逻辑再拆细一点，降低单文件维护压力。
- 还可以继续补充更细的加载态、错误提示、表单校验反馈，让页面在异常场景下更可控。

### 7. 测试与文档

涉及文件：

- `handlers/*_test.go`
- `converter/*_test.go`
- `README.md`
- `docs/`

可优化方向：

- 当前测试覆盖率已经不错，基础路径比较完整。
- 后续更值得补的是“回归型测试”，例如配置项真实生效范围、自动刷新边界场景、模板渲染边缘输入等。
- 文档层面可以继续收敛：把“已经实现的能力”和“预留但未启用的能力”写得更清楚，降低使用者理解成本。

## 项目链接

- **GitHub 仓库**: https://github.com/zhf883680/clash-subscription-manager
- **Docker Hub 镜像**: https://hub.docker.com/r/zhf883680/clash-subscription-manager

## 感谢
[tindy2013/subconverter](https://github.com/tindy2013/subconverter)
