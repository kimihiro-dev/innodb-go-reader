# 项目需求

## 问题与目标

用 Go 离线读取受支持的 MySQL InnoDB物理文件，完整还原当前存储列值，并提供可复核的中文解析手册和命令行工具。

已确认的数据还原优先原则：不将不支持的数据静默省略、填零或替换为 SQL NULL；每项解析能力随实现交付格式解释和独立验证，不逐类移植 Java 参考项目。

## 当前交付状态

- 当前实现提供十个CLI命令及[01–94章解析手册](format/README.md)；操作见[CLI指南](CLI.md)，详细兼容性和API以[支持矩阵](format/83-release-support-matrix.md)为准。
- 当前源码版本为`v0.2.0`，包含当前支持矩阵列出的全部已实现能力。
- 当前工作只在[TODO](TODO.md)记录，开发和文档规则见[CONVENTIONS](CONVENTIONS.md)。不保留发展路径、历史决议、施工计划或已完成任务流水。
- 源码标签`v0.1.0`固定提交`116de5046aa94b379ce4d2e0711e34be9766c397`，不移动；固定版本与当前源码分开。GitHub Release由用户手动创建，不推断线上发布状态。

## 输入与支持范围

详细类型、键、布局、版本组合、API和预算以[第83章支持矩阵](format/83-release-support-matrix.md)为唯一正文。本文件只保留判断输入和请求所需的概要：

| 维度 | 当前范围与入口 |
|---|---|
| MySQL 8.0 | 明确验收8.0.11/12/16/19/23/28/30/45；自动SDI发现，准确组合及旧式/现代INSTANT见[第93章](format/93-mysql80-patch-compatibility.md)；8.0.29明确排除，依据见第93章 |
| MySQL 5.7 | 明确验收5.7.9/10/11/12/28/43/44；使用可信Schema/SecondarySchema，实例驱动采集和版本/连接限制见[第92章](format/92-mysql57-patch-compatibility.md) |
| 物理快照 | 16 KiB、独立非压缩非加密表空间、crc32；目标写事务已结束，整个读取期间文件与可信定义保持稳定 |
| 行与列 | DYNAMIC/COMPACT及已验收DML/DDL组合；既有数值、文本/二进制、时间、ENUM/SET、JSON、二维空间值及SQL NULL；精度/宽度以具体版本证据为限 |
| 文本与索引 | 已验收字符集及四个 `_bin` 字符键规则；多层聚簇、复合键、实际唯一非空键、隐藏DB_ROW_ID和普通BTREE二级；5.7不推定8.0物理DESC/INSTANT/INVISIBLE |
| 生成列 | STORED物化；VIRTUAL只在显式仅物化入口单列报告，不求值或填伪NULL；8.0不可见列按版本组合验收 |
| 完整分区集合 | 普通RANGE/LIST/HASH/KEY；8.0通过SDI/manifest v1，5.7通过可信PartitionSchema/manifest v2；定义序扫描，DDL及方法边界见[第94章](format/94-partition-version-compatibility.md) |
| 离线工具 | metadata/page/export/query/check/space/stats/visualize；JSONL/CSV带类型无损导出，stats输出精确JSON/表格，HTML按请求层分别认证 |
| 输入准备 | schema通过Go MySQL驱动采集已验收5.7单表或完整分区目录，支持TCP/socket及证书验证TLS；manifest准备完整清单。仅采元数据，物理快照由外部备份工具提供 |

「支持」不意味着这些维度的任意组合均经过实测；完整类型物化单值上限仍为16 MiB。元数据预检不认证全部列值或同一事务时点，可信定义的正确性和稳定快照由调用方保证。

## 功能与技术入口

| 功能 | 当前说明 |
|---|---|
| SDI与表级动态元数据 | [第43章](format/43-sdi-schema.md) |
| 六视图统计与精确JSON | [85–86章](format/85-statistics-model-and-metrics.md) |
| 5.7可信Schema与二级读取 | [87–88章](format/87-mysql57-explicit-schema.md) |
| 系统表空间知识 | [89–90章](format/89-mysql57-system-tablespace.md)，不增加系统空间解析或恢复能力 |
| 驱动采集与清单生成 | [第91章](format/91-cli-input-preparation.md) |
| 明确5.7补丁/连接差异 | [第92章](format/92-mysql57-patch-compatibility.md) |
| 明确8.0补丁/INSTANT/LOB | [第93章](format/93-mysql80-patch-compatibility.md) |
| 两分支完整分区集合 | [第94章](format/94-partition-version-compatibility.md) |

## 验收标准

1. 全部请求列值与独立SQL/官方工具预期一致，精度、类型、NULL和空值保持区别；新版本/组合单独取得证据。
2. 物理偏移、记录/树/LOB来源可逐字节复核，固定夹具及原始证据不改写。
3. 可检测损坏、不支持或超限明确报错；完整结果失败无部分成功，流式输出核对error与Complete，文件仅完整成功后原子发布。
4. 当前范围、非目标、完成与预算契约可查；格式解释和可复制使用示例随能力交付，stdout数据与stderr诊断分离。
5. 实现运行适用回归；历史通过不算本轮重跑，源码事实、真实预期和合成测试分别标识。首版复验见[第84章](format/84-release-validation.md)，对应技术证据从[验证资料](README.md#验证资料)进入。

## 明确非目标

- 不外推未验收补丁、升级混合文件、任意类型/精度/DDL组合或其他MySQL/MariaDB版本；8.0.29继续排除后续兼容与验收。
- 不提供其他页大小、系统/通用表空间、压缩/加密或REDUNDANT支持；不将知识文档视为解析能力。
- 离线命令不隐式连接实例或采集在线快照；schema仅采元数据，不提供redo/undo回放、MVCC可见性、事务恢复或删除历史值还原。
- 不实现通用SQL/生成表达式/空间拓扑引擎或FULLTEXT/SPATIAL/函数/多值索引；分区不增加子分区、LINEAR/COLUMNS、未验收KEY算法、集合范围查询、路由或全局归并。
- 不因版本标签增加永久API兼容承诺；发布按用户授权执行。

## 文档维护与发布

当前需求、约定、任务、CLI和支持矩阵共同说明项目状态；新增实现直接更新相关正文，不追加决议或过程记录。技术解释、固定夹具、测试和独立报告保留，用于验证实现行为，资料导航见[文档目录](README.md)。

固定版本的[发布说明](releases/v0.1.0.md)与[构建指引](RELEASING.md)保持其源码范围；当前源码的新能力不回写为v0.1.0能力。
