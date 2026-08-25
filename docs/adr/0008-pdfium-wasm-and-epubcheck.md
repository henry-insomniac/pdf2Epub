# 使用 PDFium WebAssembly 和 EPUBCheck

Go 服务负责领域流程、任务调度和 EPUB 生成，通过 `go-pdfium` 的 WebAssembly 模式读取结构化文字、坐标、元数据、书签与链接并渲染页面，通过 W3C EPUBCheck 命令行工具校验最终 EPUB。该组合用 WebAssembly 隔离不受信任 PDF、避免 CGO 崩溃影响主进程，并获得纯 Go PDF 工具尚未提供的版面能力；代价是较高的转换时间、WebAssembly 内存占用以及镜像中的 Java 运行时，均由单任务并发、总超时和容器封装控制。
