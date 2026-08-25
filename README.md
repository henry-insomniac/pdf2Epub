# 纸间（btc-server）

使用 Go 构建、可部署到服务器的 PDF → 可重排 EPUB 3 服务，内置简单响应式 Web UI。V1 面向带文本层、单栏、连续正文为主的图书型 PDF；不提供 OCR，也不会把缺失正文的扫描页静默转成整页图片。

## 快速启动

推荐使用 Docker Compose：

```bash
cp .env.example .env
# 修改 .env 中的账号、密码和随机会话密钥
docker compose up --build -d
```

浏览器打开 `http://服务器地址:8080`。服务默认在容器内监听 `0.0.0.0:8080`，不是只监听 `127.0.0.1`。

公网部署应放在 Caddy、Nginx 或其他 HTTPS 反向代理后，并将 `BTC_SECURE_COOKIE=true`。账号凭据也可通过 `BTC_USERNAME_FILE`、`BTC_PASSWORD_FILE`、`BTC_SESSION_SECRET_FILE` 指向 Docker Secret 文件，避免直接放入环境变量。

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
| `BTC_USERNAME` | 必填 | 唯一内置账号 |
| `BTC_PASSWORD` | 必填 | 唯一内置账号密码 |
| `BTC_SESSION_SECRET` | 必填 | 至少 32 字节，用于会话令牌 HMAC |
| `BTC_SECURE_COOKIE` | `true` | HTTPS 部署必须保持 `true` |
| `BTC_WORK_DIR` | `/tmp/pdf2epub` | 任务临时目录，启动时会清空其中旧任务 |
| `BTC_EPUBCHECK_COMMAND` | `epubcheck` | EPUBCheck 可执行命令 |
| `BTC_REQUIRE_EPUBCHECK` | `true` | 是否缺少 EPUBCheck 时拒绝成功 |

固定限制：单文件 100 MiB、1000 页、单并发、任务超时 30 分钟、终态与成功产物保留 1 小时。

## API

- `POST /api/v1/auth/login`：登录并取得 CSRF token。
- `POST /api/v1/auth/logout`：退出。
- `GET /api/v1/session`：恢复会话。
- `POST /api/v1/jobs`：以 multipart 字段 `file` 上传一个 PDF。
- `GET /api/v1/jobs/{id}`：获取任务阶段与页数进度。
- `POST /api/v1/jobs/{id}/cancel`：取消任务。
- `GET /api/v1/jobs/{id}/download`：成功后下载 EPUB。
- `GET /healthz`：容器与反向代理健康检查。

除登录和健康检查外，任务接口都需要会话；修改操作还需要 `X-CSRF-Token`。

## 验证

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
docker build -t pdf2epub .
```

真实验收样本从 `/Users/Zhuanz/pdf` 读取，但不会复制进仓库或提交 Git。详细范围与成功标准见 [V1 验收矩阵](docs/acceptance-v1.md) 和 [领域上下文](CONTEXT.md)。
