# .claude 文档索引

`.claude` 目录保存 `btc-server` 的长期上下文，供人类维护者和 Agent 在开发、排障、评审时快速理解项目约定。

## 文档列表

- `project-architecture.md`：项目定位、目录职责、架构约束和扩展原则。
- `skill-authoring.md`：项目级 skills 的编写、安装和维护规范。
- `bug-fix-log.md`：bug 修复记录、复盘模板和已知问题。
- `git-collaboration.md`：分支命名、提交信息、PR、评审和发布约定。
- `tech-stack.md`：当前技术栈、推荐工具链、脚本和文档规范。

## 维护规则

- 修改项目结构时，同步更新 `project-architecture.md`。
- 新增、删除或重命名项目级 skill 时，同步更新 `skill-authoring.md`。
- 修复 bug 后，同步更新 `bug-fix-log.md`。
- 调整协作流程时，同步更新 `git-collaboration.md`。
- 引入新语言、运行时、包管理器、测试框架或格式化工具时，同步更新 `tech-stack.md`。
- 如果项目启用了 `docs/agents/`，调整 issue tracker、triage label 或领域上下文布局时，同步更新对应文件。



## 相关 Agent 操作规则

如果初始化时启用了 `--with-agent-ops`，以下文件用于约束 Agent 的具体操作方式：

- `../docs/agents/issue-tracker.md`：issue、PRD 和任务流转位置。
- `../docs/agents/triage-labels.md`：issue triage 状态和项目真实 label 的映射。
- `../docs/agents/domain.md`：长期上下文、领域语言和架构决策的读取规则。
- `../docs/agents/skill-usage.md`：项目级 Agent skills 的组合使用建议。

## 当前状态

本目录由脚手架在 2026-08-25 初始化。请根据项目真实情况补充架构、技术栈和验证命令。
