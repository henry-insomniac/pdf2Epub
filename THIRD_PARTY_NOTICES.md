# 第三方组件说明

本项目源码依赖的精确版本以 `go.mod` / `go.sum` 为准，容器系统包以构建时 Debian bookworm 仓库为准。以下列出运行时主要直接组件；发布镜像时仍应保留各发行包自带的完整许可证文本。

| 组件 | 固定版本 | 用途 | 许可证 |
| --- | --- | --- | --- |
| Go | 1.26.7 | 构建工具链 | BSD-3-Clause |
| `github.com/klippa-app/go-pdfium` | 1.19.8 | PDFium Go API 与 WebAssembly 运行封装 | MIT |
| PDFium WebAssembly | 随 `go-pdfium` 1.19.8 嵌入 | PDF 解析与渲染 | Apache-2.0 及 PDFium 第三方组件许可证 |
| `github.com/tetratelabs/wazero` | 1.12.0 | WebAssembly runtime | Apache-2.0 |
| W3C EPUBCheck | 5.3.0 | EPUB 规范校验 | BSD-3-Clause |
| OpenJDK JRE | Debian bookworm OpenJDK 17 | 运行 EPUBCheck | GPL-2.0 with Classpath Exception |

间接 Go 依赖包括 `google/uuid`、`go-commons-pool/v2` 以及 `golang.org/x/*`；其版本和校验值记录在 Go Modules 文件中。
