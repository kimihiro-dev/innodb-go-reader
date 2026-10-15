# InnoDB Go Reader

用Go离线读取受支持的MySQL InnoDB物理文件，解析当前存储列、索引和空间结构。模块为`github.com/kimihiro-dev/innodb-go-reader`，根包名`innodb`；根包、可选`rowio`和`stats`均只依赖标准库。

## 库使用

需要Go 1.25或更高版本。调用方管理io.ReaderAt及文件生命周期，保证物理输入和可信定义在调用期间稳定。8.0可自动发现SDI，5.7使用可信定义；准确版本和组合见[支持矩阵](docs/format/83-release-support-matrix.md)。

首次引入当前模块可选择最新主分支，Go会在调用方模块文件中记录实际版本；已有历史标签不代表当前模块边界：

```sh
go get github.com/kimihiro-dev/innodb-go-reader@main
```

```go
import innodb "github.com/kimihiro-dev/innodb-go-reader"

result, err := innodb.ReadAuto(reader, size)
if err != nil {
    return err
}
for _, record := range result.Records {
    consume(record.Values)
}
```

上例中的reader、size和consume由调用方提供。Read保留完整结果，Scan系列按行交付；流式消费者同时检查error与Complete。Diagnose及一致性报告还需区分Complete与Passed，局部/投影读取不认证未请求范围。

```sh
go run ./examples/read testdata/mysql8045/lesson_rows.ibd testdata/mysql8045/lesson_rows.json
go build ./...
go test -race -timeout=25m ./...
go vet ./...
```

## 能力与文档

- 元数据与严格读取：SDI、可信Schema、聚簇/二级索引、当前列值和LOB。
- 按需访问：流式扫描、点查/范围、列投影、原始大LOB和完整分区集合。
- 解释与校验：局部页、字段来源、结构化诊断、行列存储分析及有可信证据的索引交叉校验。
- 辅助包：`rowio`提供带类型无损JSONL/CSV，`stats`提供精确空间统计。
- [当前需求](docs/REQUIREMENTS.md)、[支持矩阵](docs/format/83-release-support-matrix.md)、[01–100章手册](docs/format/README.md)、[文档导航](docs/README.md)。
- [项目边界与联合开发](docs/LIBRARY_ASSESSMENT.md)、[开发约定](docs/CONVENTIONS.md)、[验证与库交付](docs/RELEASING.md)。

## CLI与可视化

十个命令、实例元数据采集和HTML报告已迁入独立的[innodb-go-reader-cli](https://github.com/kimihiro-dev/innodb-go-reader-cli)。本仓库不再包含cmd、采集器或HTML实现；CLI二进制仍名为innodb-reader。本地同级目录的联合开发方式见项目边界文档。

旧导入`innodb-go-reader`须改为正式模块路径，rowio/stats沿用相应子包名；旧公开visual包迁为CLI内部实现，不保留转发层。外部消费者通过正式模块路径依赖已推送的库提交或版本；本地联合开发使用Go工作区。

## 支持边界

明确验收的5.7/8.0补丁、16KiB、独立非压缩非加密表空间，DYNAMIC/COMPACT；不推定任意类型/DDL/版本组合均受支持。完整物化单值上限16MiB，StreamLOB另按预算读取可信原始引用。不提供系统/通用表空间解析、redo/undo/MVCC恢复、通用SQL、VIRTUAL求值或双快照比较。物理格式与成功范围以支持矩阵为准。

开发跟随当前分支最新提交，已有标签只标识历史版本；构建及验证记录实际源码，固定夹具与独立证据保持原样。
