# 文档导航

先按用途选择入口。当前功能以需求和第 83 章矩阵为准；历史阶段的「当前 / 尚未支持」仅说明当时的边界。

## 使用与判断支持范围

| 需求 | 文档 | 正文职责 |
|---|---|---|
| 运行命令、查询、导出、分区与失败处理 | [CLI.md](CLI.md) | 操作步骤与可复制示例 |
| 判断文件/类型/布局是否支持 | [支持矩阵](format/83-release-support-matrix.md) | 当前详细兼容性及 API/资源契约 |
| 学习字节布局和逐步解码 | [解析手册](format/README.md) | 01–84 章教材与真实样本 |
| 构建、打包并手动发布 | [RELEASING.md](RELEASING.md) | 固定标签的多平台命令 |
| 复制 v0.1.0 发布描述 | [发布说明](releases/v0.1.0.md) | 对外版本概要，不复制内部施工日志 |
| 复跑首版验收、定位故障 | [第 84 章](format/84-release-validation.md) | 验证命令、证据索引及限制 |

## 项目状态与维护

| 文件 | 唯一职责 | 不应追加的内容 |
|---|---|---|
| [REQUIREMENTS.md](REQUIREMENTS.md) | 当前问题、范围、验收与非目标 | 各阶段施工日记或详细测试数字 |
| [DECISIONS.md](DECISIONS.md) | 现行选择、备选理由与替代关系 | 成批复制实现说明和验收日志 |
| [TODO.md](TODO.md) | 有编号的任务、状态、日期和证据入口 | 重复验收正文 |
| [CONVENTIONS.md](CONVENTIONS.md) | 实施约束、文档职责/格式及检查流程 | 早期阶段已被覆盖的支持限制 |
| [STAGE_PLANS.md](STAGE_PLANS.md) | 完整阶段问题、范围、备选与验收计划 | 活跃状态猜测或另一份验证日报 |

## 历史与原始证据

- [HISTORY.md](HISTORY.md)：各阶段验收正文的集中记录，含数量、测试结果和当时边界。
- [历史决议](history/DECISIONS.md)：保留施工期澄清、备选和实现取舍，不作为现行规范。
- [Java 参考调研](REFERENCE_ANALYSIS.md)：2026-09-09 的参考实现结论，不代表当前 Go 支持矩阵。
- [原始首版验证报告](release-validation/report.json)及同目录日志：原始证据，不按文档格式重写。

后续改文档按 [维护规则](CONVENTIONS.md#文档维护)执行，并在仓库根目录运行 `python3 scripts/check_docs.py`。
