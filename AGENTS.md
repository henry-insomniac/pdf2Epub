# btc-server Agent Guide

## 项目概述

`btc-server`：可部署到服务器、带简单 Web UI 的 PDF 转可重排 EPUB 3 服务

本文件是 Agent 进入项目后的第一入口。项目的长期上下文、架构说明、协作规范和技术规范集中维护在 `.claude` 目录。

## 详情索引

项目级细节统一维护在 `.claude` 目录：

- `.claude/README.md`：文档索引和维护规则。
- `.claude/project-architecture.md`：项目架构、目录职责和扩展方式。
- `.claude/skill-authoring.md`：项目级 skills 的编写、安装和维护规范。
- `.claude/bug-fix-log.md`：bug 修复记录和复盘模板。
- `.claude/git-collaboration.md`：分支、提交、PR 和代码评审规范。
- `.claude/tech-stack.md`：项目相关技术栈、工具链和技术规范。




## Agent 工作原则

- 先阅读本文件和 `.claude/README.md`，再修改项目结构或新增规范。
- 修改项目结构时，同步更新 `.claude/project-architecture.md`。
- 新增或调整项目级 skill 时，同步更新 `.claude/skill-authoring.md`。
- 修复问题后更新 `.claude/bug-fix-log.md`，包含现象、原因、修复方式和验证结果。
- 保持改动聚焦，避免把无关重构、格式化或命名调整混入同一个变更。
- 记录重要决策的原因，尤其是目录结构、依赖工具、协作流程和外部服务边界的变化。

## 维护要求

- 文档使用中文为主，必要的命令、文件名、API 名称保持英文原文。
- 引入外部依赖前，先说明用途、替代方案和维护成本。
- 涉及用户数据、密钥、账号状态或外部服务的流程，必须显式写出安全边界。
- 有可执行验证命令时，完成变更后必须运行并记录结果。

## 初始化信息

- 初始化日期：2026-08-25
- 脚手架来源：`insomniac-skills`
