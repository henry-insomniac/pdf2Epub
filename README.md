# 纸间（btc-server）

使用 Go 构建、可部署到服务器的 PDF → EPUB 3 服务，内置简单响应式 Web UI。服务可自动选择输出：正文型 PDF 转为可重排 EPUB，图片型或复杂版式 PDF 转为保留原页面的固定版式双页 EPUB。固定版式不提供 OCR；手动选择可重排时不会把缺失正文的扫描页静默转成图片。

## 快速启动

推荐使用 Docker Compose：

```bash
cp .env.example .env
# 私有模式：修改 .env 中的账号、密码和随机会话密钥
docker compose up --build -d
```

服务默认在容器内监听 `0.0.0.0:8080`，但 Compose 出于安全考虑只将其发布到宿主机 `127.0.0.1:8080`，供同机 Nginx/Caddy 反向代理。若明确需要直接暴露测试端口，可将 `BTC_BIND_IP=0.0.0.0`。

公网部署应放在 Caddy、Nginx 或其他 HTTPS 反向代理后，并将 `BTC_SECURE_COOKIE=true`。账号凭据也可通过 `BTC_USERNAME_FILE`、`BTC_PASSWORD_FILE`、`BTC_SESSION_SECRET_FILE` 指向 Docker Secret 文件，避免直接放入环境变量。

生产环境推荐配置 Cloudflare R2 交付成功产物。服务会在转换完成后将 EPUB 上传到私有 bucket；已登录用户点击下载时，API 返回短期签名地址，文件流不再经过应用服务器。私有模式下 R2 不可用会保留本地产物作为降级；公开付费模式则失败并自动退回额度，绝不回退到源站大文件下载。

## 公开付费 Beta

设置 `BTC_PUBLIC_ACCESS=true` 后，页面不再要求共享账号密码，而是为浏览器签发 HMAC 保护的访客会话。每个任务在服务端原子扣除 1 次额度，失败或取消时幂等退款；支付成功页本身不会充值，只有通过 Stripe 验签并与本地订单绑定的 webhook 才能入账。

公开模式是 fail-closed 的：缺少 HTTPS 公网地址、Stripe secret/webhook secret/price、Cloudflare Turnstile、私有 R2、Secure Cookie 或持久化账本路径时，服务拒绝启动。最低上线步骤：

1. 在 Stripe 创建固定 Price，并把其 ID、secret key 与 webhook signing secret 写入服务器 secret；webhook 地址为 `https://你的域名/api/v1/billing/webhook`。
2. 在 Cloudflare Turnstile 创建只允许正式域名的 Managed widget，配置 site key 和 secret key。
3. 配置私有 R2 bucket 与仅限该 bucket 对象读写的密钥；禁止公开 bucket。
4. 将源站端口保持在回环地址或防火墙后，由 Cloudflare 和 HTTPS 反向代理接入；Nginx 对上传路径必须设置 `proxy_request_buffering off`，让 Go 在读取大文件前校验一次性上传票据。公开默认 90 MiB，为 Cloudflare 的 [100 MB 请求体限制](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/#upload-limits)预留 multipart 开销。
5. 核对 `BTC_CREDIT_PACK_LABEL` 与 Stripe Price 的真实币种和金额一致，并完成支付、重复 webhook、取消/失败退款、越权下载和队列满载演练。

当前 Beta 的额度身份绑定浏览器 Cookie：清除 Cookie 或更换设备后无法自助找回余额。面向大规模陌生用户投放前，应补充邮件 magic link 的账号恢复、服务条款、隐私政策、退款政策和客服入口；不要把共享管理员登录重新用作用户账号系统。

完整威胁边界与上线阻断项见 [公开付费 Beta 安全清单](docs/security-public-beta.md)。

## 本地开发

需要 Go 1.26.7 和 EPUBCheck 5.x。若只是调试解析与 UI，可以暂时跳过外部 EPUBCheck；生产环境和 Docker 镜像始终强制校验。

```bash
export BTC_USERNAME=admin
export BTC_PASSWORD='local-password'
export BTC_SESSION_SECRET='replace-with-at-least-32-random-bytes'
export BTC_SECURE_COOKIE=false
export BTC_REQUIRE_EPUBCHECK=false
go run ./cmd/btc-server
```

然后访问 `http://localhost:8080`。若已安装 EPUBCheck，保持 `BTC_REQUIRE_EPUBCHECK=true`（默认值）并通过 `BTC_EPUBCHECK_COMMAND` 指定命令路径。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `BTC_ADDR` | `0.0.0.0:8080` | HTTP 监听地址，可部署到服务器 |
| `BTC_PUBLIC_ACCESS` | `false` | 启用无密码访客、额度和支付；安全配置不完整时拒绝启动 |
| `BTC_PUBLIC_URL` | 空 | 公开模式的 HTTPS origin，例如 `https://epub.yi-flow.com` |
| `BTC_USERNAME` | 私有模式必填 | 私有模式唯一内置账号；公开模式不使用 |
| `BTC_PASSWORD` | 私有模式必填 | 私有模式唯一内置账号密码；公开模式不使用 |
| `BTC_SESSION_SECRET` | 必填 | 至少 32 字节，用于会话令牌 HMAC |
| `BTC_SECURE_COOKIE` | `true` | HTTPS 部署必须保持 `true` |
| `BTC_WORK_DIR` | `/tmp/pdf2epub` | 任务临时目录，启动时会清空其中旧任务 |
| `BTC_MAX_UPLOAD_MIB` | 私有 100 / 公开 90 | 上传上限；公开默认值为 Cloudflare 100 MB 请求限制预留 multipart 空间 |
| `BTC_QUEUE_CAPACITY` | 私有 0 / 公开 3 | 单工作器之外允许等待的任务数，最大 20 |
| `BTC_COMMERCE_DB_PATH` | `/tmp/pdf2epub-commerce/commerce.db` | bbolt 额度、订单、回调和任务扣费账本；不得放在会被启动清理的工作目录 |
| `BTC_CREDIT_PACK_CREDITS` | `5` | 每个 Stripe Price 对应的转换次数 |
| `BTC_CREDIT_PACK_LABEL` | `USD 1.99` | UI 展示价格，必须人工核对与 Stripe Price 一致 |
| `BTC_STRIPE_SECRET_KEY` | 空 | 公开模式必填，只能由服务器读取 |
| `BTC_STRIPE_WEBHOOK_SECRET` | 空 | 公开模式必填，用于 webhook HMAC 验签 |
| `BTC_STRIPE_PRICE_ID` | 空 | 公开模式必填，固定额度包的 Stripe Price ID |
| `BTC_TURNSTILE_SITE_KEY` | 空 | 公开模式必填，可下发浏览器 |
| `BTC_TURNSTILE_SECRET_KEY` | 空 | 公开模式必填，只能由服务器读取 |
| `BTC_EPUBCHECK_COMMAND` | `epubcheck` | EPUBCheck 可执行命令 |
| `BTC_REQUIRE_EPUBCHECK` | `true` | 是否缺少 EPUBCheck 时拒绝成功 |
| `BTC_FIXED_LAYOUT_DPI` | `144` | 固定版式页面渲染 DPI，允许 72–200 |
| `BTC_R2_ACCOUNT_ID` | 空 | Cloudflare account ID；与下面三个 R2 变量同时设置才会启用 |
| `BTC_R2_ACCESS_KEY_ID` | 空 | 仅限产物 bucket 的 R2 S3 access key ID |
| `BTC_R2_SECRET_ACCESS_KEY` | 空 | R2 S3 secret access key，禁止提交到仓库 |
| `BTC_R2_BUCKET` | 空 | 私有 R2 bucket 名称 |
| `BTC_R2_PREFIX` | `epub` | R2 对象 key 前缀 |
| `BTC_DOWNLOAD_URL_TTL` | `15m` | 登录校验后生成的 R2 签名下载地址有效期 |

固定限制：最多 1000 页、单工作器、任务超时 30 分钟、终态与成功产物保留 1 小时。公开默认最多等待 3 个任务，队列满时返回 409；私有模式默认不排队。R2 凭据应只授予目标 bucket 的对象读写权限；bucket 保持私有，签名 URL 视为临时 bearer token。

## API

- `POST /api/v1/auth/login`：登录并取得 CSRF token。
- `POST /api/v1/auth/guest`：公开模式建立无密码访客会话。
- `GET /api/v1/meta`：读取是否启用公开模式。
- `POST /api/v1/auth/logout`：退出。
- `GET /api/v1/session`：恢复会话。
- `POST /api/v1/upload-tickets`：公开模式使用 Turnstile token 换取两分钟、一次性的上传票据。
- `POST /api/v1/billing/checkout`：创建固定额度包的 Stripe Checkout。
- `POST /api/v1/billing/webhook`：Stripe 验签与幂等入账入口。
- `POST /api/v1/jobs`：以 multipart 字段 `file` 上传一个 PDF；`mode` 可为 `auto`、`reflowable` 或 `fixed`，默认 `auto`。
- `GET /api/v1/jobs/{id}`：获取任务阶段与页数进度。
- `POST /api/v1/jobs/{id}/cancel`：取消任务。
- `GET /api/v1/jobs/{id}/download`：成功后下载 EPUB。
- `GET /healthz`：容器与反向代理健康检查。

除 meta、登录/访客建会话、支付 webhook 和健康检查外，任务接口都需要会话；修改操作还需要 `X-CSRF-Token`。任务状态、取消和下载同时校验任务 owner，owner 不匹配统一返回 404。

## 验证

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
docker build -t pdf2epub .
```

真实验收样本从 `/Users/Zhuanz/pdf` 读取，但不会复制进仓库或提交 Git。详细范围与成功标准见 [V1 验收矩阵](docs/acceptance-v1.md) 和 [领域上下文](CONTEXT.md)。
