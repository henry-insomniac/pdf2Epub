# 公开付费 Beta 安全与上线清单

## 请求链路

```text
浏览器
  → Cloudflare WAF / Rate Limiting / DDoS
  → HTTPS 反向代理（源站端口不公网暴露）
  → Go 会话、CSRF、ALTCHA/Turnstile、IP/subject 限流、额度与一次性上传票据
  → 单工作器 + 有限等待队列
  → PDFium WASM / EPUBCheck
  → 私有 R2 短时签名下载

额度码 → HMAC 验签与单次入账/钱包恢复 → bbolt 额度账本
Stripe（可选）→ Cloudflare/HTTPS → webhook 验签 → bbolt 幂等订单与额度账本
```

## 必须满足

- DNS 使用 Cloudflare 代理；防火墙只开放 80/443，应用端口只绑定 `127.0.0.1`。若源站允许绕过 Cloudflare，不能信任 `CF-Connecting-IP` 或 `X-Real-IP`。
- Nginx/Caddy 只向 Go 传入自己确定的客户端 IP header；清除客户端自带的冲突 header。
- Nginx 上传 location 设置 `proxy_request_buffering off`。否则 Nginx 会先接收完整 PDF，Go 的上传票据即使在读 body 前校验，也无法阻止攻击者消耗反向代理带宽和磁盘。
- Cloudflare 计划的请求体上限必须覆盖完整 multipart 请求。公开模式默认 90 MiB，为 Cloudflare 文档列出的 [100 MB 请求体上限](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/#upload-limits)预留协议开销；升级计划后才提高 `BTC_MAX_UPLOAD_MIB`。
- ALTCHA 模式使用至少 32 字节服务端 secret 派生独立签名密钥，挑战短时有效，成功 payload 拒绝重放；它不代替 WAF、限流、额度和资源隔离。前端 widget 固定版本 CDN 是供应链与可用性依赖，CSP 只允许明确域名及其 bundle 所需的 `blob:` worker，后续宜纳入镜像。
- Turnstile 模式下 widget 限定正式 hostname；secret 只在服务端，token 每次调用 [Siteverify](https://developers.cloudflare.com/cloudflare-challenges/challenge-types/turnstile/#implementation) 且不复用。
- `BTC_VOUCHER_SECRET` 必须加密备份且不可轮换后直接丢弃；完整额度码不进入应用日志。额度码泄露等同对应钱包被接管，销售渠道必须私密交付。
- Stripe webhook endpoint 只接受原始 body 验签；禁止用 success URL、query 参数或前端提交的价格/额度入账。
- R2 bucket 保持私有，token 只授予目标 bucket 对象读写；签名 URL TTL 保持短时，日志不记录完整 URL。
- `pdf2epub-data` volume 定期加密备份，并演练恢复；不得把 `commerce.db` 放入会在启动时清空的 `BTC_WORK_DIR`。
- 告警至少覆盖：5xx、429/402/409 比例、队列深度、任务超时、转换失败、webhook 验签失败、账本写入失败、R2 发布失败和磁盘空间。公开模式的 R2 发布失败必须退款且不能回退源站下载。

## 上线阻断项

- Voucher 模式未完成生成、首次兑换、跨浏览器恢复和泄露处置演练；或 Stripe 模式未完成真实 Price、webhook 与端到端支付。
- ALTCHA 模式允许 payload 重放，或 Turnstile 模式未限定正式 hostname / Siteverify 故障时仍允许上传。
- 源站 IP/应用端口可被公网直接访问。
- 没有隐私政策、服务条款、退款政策、客服与账本备份。
- 没有明确提示“额度码等同钱包恢复凭证”，或没有遗失/泄露后的人工处理流程。
