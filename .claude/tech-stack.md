# 技术栈与技术规范

## 当前状态

`btc-server` 使用 Go 构建可部署的 PDF 转 EPUB Web 服务。正式交付 Linux Docker 镜像和 `docker compose` 示例，生产 HTTPS 由反向代理或云负载均衡器终止。

## 文档规范

- 主要文档使用 Markdown。
- 中文为主，命令、文件名、API、包名保留英文原文。
- 文件名使用小写短横线，顶层约定文件除外，例如 `AGENTS.md`。
- 文档标题层级从一个一级标题开始，不跳级。
- 命令、路径、环境变量使用反引号标记。



## 代码规范

- 语言与工具链：Go 1.26.7。
- HTTP：标准库 `net/http` 与 `http.ServeMux`。
- Web UI：原生 HTML、CSS、JavaScript，通过 `go:embed` 嵌入服务；不使用 Node.js 或独立前端构建链。
- API：同源 JSON HTTP API。
- 部署：Linux Docker 镜像与 `docker compose`。
- 格式化：Go 代码必须通过 `gofmt`。
- PDF 引擎：`go-pdfium` WebAssembly 模式，禁止默认挂载整个容器文件系统。
- EPUB 生成：Go 代码生成 EPUB 3 包。
- EPUB 校验：W3C EPUBCheck 命令行工具和最小 Java 运行时，由 Docker 镜像封装。
- 产物交付：可选 Cloudflare R2 私有 bucket，通过 AWS SDK for Go v2 的 S3 兼容 API 上传、生成短期签名 URL 并清理对象。
- 构建必须固定 PDFium、`go-pdfium`、EPUBCheck 和 Java 运行时版本，并输出第三方许可证清单。

- 依赖管理：Go Modules，`go.mod` 固定直接依赖版本。
- 测试：标准库 `testing`，HTTP 使用 `httptest`。
- Java：Docker 使用 Debian bookworm 的 OpenJDK 17 JRE；EPUBCheck 固定为 5.3.0 并校验下载 SHA-256。

## 脚本规范

- 脚本必须支持从仓库根目录运行，或在文档中明确工作目录。
- 脚本失败时应返回非零退出码，并输出可定位问题的信息。
- 不在脚本中硬编码个人绝对路径。
- 涉及外部服务的脚本必须说明鉴权方式、权限边界和失败处理。

## 依赖规范

新增依赖前需要说明：

- 依赖解决什么问题。
- 是否已有项目内工具、系统工具或标准库可替代。
- 是否会增加安装、运行或维护成本。
- 是否需要网络、账号或密钥。

## 安全规范

- 不提交 `.env`、密钥、令牌、Cookie、账号凭据。
- 示例配置使用 `.env.example` 或文档片段，不使用真实值。
- 涉及外部 API 的流程必须说明鉴权方式和权限边界。
- R2 token 只允许目标产物 bucket 的对象读写，不授予 bucket 管理权限；bucket 不开启匿名访问。
- 涉及文件删除、发布、推送、远程写入的操作必须有明确前置检查。

## 验证规范

```bash
gofmt -w cmd internal
go vet ./...
go test ./...
go test -race ./...
docker build -t pdf2epub .
```
