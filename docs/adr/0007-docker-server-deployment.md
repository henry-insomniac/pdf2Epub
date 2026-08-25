# V1 使用 Docker 部署

V1 以 Linux Docker 镜像和 `docker compose` 示例作为正式服务器交付物，容器内允许监听 `0.0.0.0`，并由 Caddy、Nginx 或云负载均衡器终止 HTTPS 后转发请求。PDF 处理与 EPUB 校验所需的系统依赖封装进镜像，避免每台服务器手工安装不一致的工具链。
