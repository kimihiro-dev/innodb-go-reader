# 文档导航

以当前源码、[需求](REQUIREMENTS.md)、[开发约定](CONVENTIONS.md)和[支持矩阵](format/83-release-support-matrix.md)理解项目。当前工作见[TODO](TODO.md)，源码以最新提交为准，已有标签仅表示历史发布。

## 使用与支持范围

| 需求 | 入口 |
|---|---|
| 十个命令、输入准备、查询/导出/分区和失败处理 | [CLI指南](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md) |
| 当前范围、验收与非目标 | [REQUIREMENTS](REQUIREMENTS.md) |
| 基础库与应用边界、导入迁移和联合开发 | [项目边界](LIBRARY_ASSESSMENT.md) |
| 详细版本/类型/布局、API与资源契约 | [支持矩阵](format/83-release-support-matrix.md) |
| 物理字节与逐步解码 | [01–100章手册](format/README.md) |
| 开发、测试及文档规则 | [CONVENTIONS](CONVENTIONS.md)、[当前任务](TODO.md) |
| 最新提交编译与手动发布 | [构建指引](RELEASING.md)、[v0.1.0说明](releases/v0.1.0.md) |

## 功能专题

| 主题 | 技术与使用入口 |
|---|---|
| 统计汇总与精确JSON | [CLI stats](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#stats统计汇总与-json)、[85–86章](format/85-statistics-model-and-metrics.md) |
| 5.7可信表/二级定义 | [CLI显式Schema](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#mysql-5744显式-schema)、[87–88章](format/87-mysql57-explicit-schema.md) |
| 系统表空间知识 | [5.7第89章](format/89-mysql57-system-tablespace.md)、[8.0第90章](format/90-mysql80-system-tablespace.md)；知识覆盖不等于解析支持 |
| 驱动采集与清单生成 | [CLI schema](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#schema采集可信定义)、[manifest](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#manifest生成分区清单)、[第91章](format/91-cli-input-preparation.md) |
| 明确5.7补丁及连接差异 | [第92章](format/92-mysql57-patch-compatibility.md) |
| 明确8.0补丁及INSTANT/LOB | [第93章](format/93-mysql80-patch-compatibility.md)；8.0.29不纳入兼容范围 |
| 5.7与8.0完整分区 | [第94章](format/94-partition-version-compatibility.md) |
| 原始/校验页与本地记录 | [第95章](format/95-local-page-reading.md)、[最小示例](../examples/page/main.go) |
| 字段物理区间及默认来源 | [第96章](format/96-field-provenance.md)、[追踪示例](../examples/trace/main.go) |
| 聚簇列投影及按需LOB | [第97章](format/97-clustered-projection.md)、[投影示例](../examples/projection/main.go) |
| 结构化问题及分层诊断 | [第98章](format/98-structured-diagnostics.md)、[诊断示例](../examples/diagnose/main.go) |
| 行列载荷、公共开销及LOB去重 | [第99章](format/99-row-column-storage.md)、[分析示例](../examples/storage/main.go) |
| 可信快照下双向索引一致性 | [第100章](format/100-index-consistency.md)、[校验示例](../examples/consistency/main.go) |

## 验证资料

固定夹具、独立SQL/官方预期、原始报告/日志和测试脚本用于复核实现，不作为施工流水或当前支持承诺。已保存结果不代表本次重新执行，当前支持以矩阵为准。

| 资料 | 内容 |
|---|---|
| [release-validation/report.json](release-validation/report.json) | 固定首版复验/构建/性能；复跑见[第84章](format/84-release-validation.md) |
| [mysql57-validation/report.json](mysql57-validation/report.json)、[secondary/report.json](mysql57-validation/secondary/report.json) | 5.7.44可信表/二级定义、SQL与物理读取验证 |
| [system-tablespace-validation](system-tablespace-validation/README.md) | 系统空间源码/干净样本字节核对 |
| [input-preparation-validation](input-preparation-validation/README.md) | 采集/清单固定对照资料；不定义当前CLI参数 |
| [schema-driver-validation](schema-driver-validation/README.md) | 驱动TCP/socket/TLS、SQL定义和失败边界 |
| [mysql57-patch-validation](mysql57-patch-validation/README.md) | 明确5.7补丁源码/实例/SQL和连接差异 |
| [mysql80-patch-validation](mysql80-patch-validation/README.md) | 明确8.0补丁SDI/INSTANT/LOB和失败验证 |
| [partition-patch-validation](partition-patch-validation/README.md) | 两分支集合、目录/DDL/SQL及官方校验 |
| [统计测试](../stats/report_test.go)、[CLI统计测试](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/internal/cli/stats_test.go) | 六视图、精度、筛选、输出与取消边界 |

资料中的版本、范围和环境分别标明；原始数据不按文风重写。新增实现同步当前正文与对应测试/证据，不恢复决议、历史计划或已完成任务记录。
