# Agent Instructions Compatibility Entry

本仓库的**唯一完整 Agent 操作契约**是 [`AGENT.md`](AGENT.md)。

某些开发工具默认查找复数形式 `AGENTS.md`，因此保留本兼容入口。开始任何工作前必须：

1. 完整阅读根目录 [`AGENT.md`](AGENT.md)。
2. 阅读 [`docs/README.md`](docs/README.md) 指向的相关设计、决策、记忆和验证文档。
3. 按 `AGENT.md` 的 Git 防丢失纪律执行：代码检查点先 commit 并 push，再做测试和其他操作；不得把 push 描述为验证通过。

若本文件与 `AGENT.md` 冲突，以 `AGENT.md` 为准。不要在此复制或维护第二套规则。