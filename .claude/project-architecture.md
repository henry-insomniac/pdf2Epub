# 项目架构

## 项目定位

`btc-server`：电子书格式转换服务

本文件用于记录项目边界、目录职责、关键架构决策和后续扩展原则。内容应以项目真实结构为准，避免只保留通用模板。

## 当前目录结构

请在项目初始化后补充真实目录结构。

```text
.
├── AGENTS.md
└── .claude/
    ├── README.md
    ├── project-architecture.md
    ├── skill-authoring.md
    ├── bug-fix-log.md
    ├── git-collaboration.md
    └── tech-stack.md
```



如果初始化时启用了 `--with-agent-ops`，还会根据参数生成：

```text
.
├── CONTEXT.md 或 CONTEXT-MAP.md
├── docs/
│   ├── agents/
│   │   ├── README.md
│   │   ├── issue-tracker.md
│   │   ├── triage-labels.md
│   │   ├── domain.md
│   │   └── skill-usage.md
│   └── adr/
│       └── README.md
└── .scratch/
    └── README.md
```

`.scratch/` 只在 `--issue-tracker local` 时生成。
`claude-only` 布局不生成 `CONTEXT.md` 或 `CONTEXT-MAP.md`。

## 目录职责

### `AGENTS.md`

Agent 入口文件。用于说明项目目标、协作原则和关键文档索引。任何 Agent 开始工作前都应先阅读该文件。

### `.claude/`

项目长期上下文目录。这里保存架构、规范、协作流程和故障记录，避免重要信息散落在对话或临时笔记中。

### `docs/agents/`

可选的 Agent 操作规则目录。只有在初始化时启用 `--with-agent-ops` 才会创建。用于说明 issue tracker、triage label、领域文档读取顺序和 skill 使用方式。

### `CONTEXT.md` / `CONTEXT-MAP.md`

可选的领域上下文入口。`single` 布局使用根目录 `CONTEXT.md`；`multi` 布局使用根目录 `CONTEXT-MAP.md` 指向多个上下文；`claude-only` 布局不创建这些文件。

### `docs/adr/`

可选的架构决策记录目录。用于保存重要技术选择、替代方案和后续维护影响。

### `.agents/skills/`

可选的项目级 Agent Skills 目录。只有在项目明确需要可复用 Agent 工作流时才创建。新增 skill 时，应同步说明触发条件、输入输出、验证方式和安全边界。

## 架构原则

- 让目录结构表达职责边界。
- 优先遵循项目已有模式，不为了新功能随意引入新风格。
- 共享逻辑需要有清晰调用边界和验证方式。
- 外部服务、账号、密钥、网络访问和数据写入必须明确安全边界。
- Agent 操作规则应写入 `docs/agents/`，项目事实和长期上下文应写入 `.claude/` 或 `CONTEXT.md`，不要混淆职责。
- 项目级 skills 应保持触发条件明确，避免把泛用提示词或个人偏好写成长期能力。
- 架构变更必须同步更新本文件。

## 架构变更记录

| 日期 | 变更 | 原因 | 验证 |
| --- | --- | --- | --- |
| 2026-08-25 | 初始化 Agent 项目文档 | 建立项目长期上下文和协作基线 | 已创建 `AGENTS.md` 与 `.claude` 文档 |
