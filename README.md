# InnoDB Go Reader

用Go离线读取受支持的InnoDB物理快照，解析当前存储列、索引和空间结构，并提供中文解析手册及命令行工具。物理解析使用标准库，实例Schema采集使用go.mod锁定的MySQL驱动。

## 从这里开始

- [命令行使用指南](docs/CLI.md)：十个命令、输入准备、查询、导出和失败处理。
- [当前需求](docs/REQUIREMENTS.md)与[支持矩阵](docs/format/83-release-support-matrix.md)：准确版本、类型/布局组合、API和资源契约。
- [01–94章解析手册](docs/format/README.md)：真实字节、逐步解码及验证方法。
- [文档导航与验证资料](docs/README.md)、[开发约定](docs/CONVENTIONS.md)、[当前任务](docs/TODO.md)。

当前源码版本为[`v0.2.0`](https://github.com/kimihiro-dev/innodb-go-reader/tree/v0.2.0)，包含当前矩阵中的统计、输入准备和明确补丁/分区兼容。固定源码版本[`v0.1.0`](https://github.com/kimihiro-dev/innodb-go-reader/tree/v0.1.0)，发布说明与多平台命令见[发布指引](docs/RELEASING.md)。当前源码包含标签之后的能力，具体差异见支持矩阵。

## 正式命令行与无损导出

需要Go 1.25或更高版本，并取得go.mod中的锁定依赖。在项目目录运行：

```sh
go build -o /tmp/innodb-reader ./cmd/innodb-reader
/tmp/innodb-reader metadata testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader check --scope rows testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader export --format jsonl --physical testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader stats --view indexes testdata/mysql8045/lesson_rows.ibd
```

| 命令 | 用途 |
|---|---|
| metadata / page | 元数据发现/身份预检及页信息 |
| export / query | 当前行导出及聚簇/二级查询 |
| check / space | 明确请求范围的严格校验及空间分析 |
| stats / visualize | 六类统计视图及分层离线HTML |
| schema / manifest | 驱动采集可信定义及准备完整分区清单 |

JSONL/CSV采用带类型的无损协议，可由公开`rowio`包读回；stats输出精确整数字符串和比例分子/分母。stdout只写数据，stderr写执行摘要；`--output`完整成功后原子发布，默认不覆盖。JSONL须核对end，CSV须核对执行状态，流式失败可能保留前缀。

5.7单表使用可信`--schema`及可选`--indexes`；8.0自动发现SDI。完整分区使用`--manifest`，分别采用8.0 SDI清单v1或5.7可信目录清单v2。Schema命令仅采元数据，物理快照由外部备份工具提供；操作及前提以CLI指南为准。

Go读取例子：

```sh
go run ./examples/read testdata/mysql8045/lesson_rows.ibd testdata/mysql8045/lesson_rows.json
```

本例按聚簇键及schema列序输出：

```json
[-3,42,"InnoDB"]
[2,null,null]
[7,0,""]
[12,-5,"你好"]
```

`Read/ReadAuto`保留完整结果，`Scan`系列按行交付；调用方管理ReaderAt及文件生命周期并保证定义/快照稳定。不同入口的成功范围、LOB物化、预算和取消边界见支持矩阵。

## 支持边界

- 明确验收的MySQL 5.7与8.0补丁、16 KiB、独立非压缩非加密表空间，DYNAMIC/COMPACT；版本白名单和具体组合见[当前矩阵](docs/format/83-release-support-matrix.md)。8.0.29不纳入兼容范围。
- 支持已有数值、文本/二进制、时间、ENUM/SET、JSON、二维空间值、NULL及已验收的聚簇/普通二级、当前LOB、DML/DDL、生成列和分区组合；不推定任意类型/精度/版本组合均可读。
- 不提供系统/通用表空间解析、压缩/加密、redo/undo/MVCC恢复、通用SQL或VIRTUAL表达式求值；系统空间教材不代表解析支持。
- 完整类型物化单值上限仍为16 MiB；元数据或空间分析成功不等于列值、事务可见性及全部索引树均已认证。

验证用`go test ./...`；按修改范围选择相关测试及[复验命令](docs/format/84-release-validation.md)。当前文档直接描述实现状态，不维护决议、历史施工计划或已完成任务流水。
