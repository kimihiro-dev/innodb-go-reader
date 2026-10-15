# 基础库与应用边界

## 项目职责

问题：基础库需要独立被Go程序依赖，命令行、实例采集和HTML需要独立开发和交付。

| 项目 | 模块及内容 |
|---|---|
| innodb-go-reader | `github.com/kimihiro-dev/innodb-go-reader`；根包innodb、rowio、stats、18个调用示例、完整核心测试及物理证据 |
| innodb-go-reader-cli | `github.com/kimihiro-dev/innodb-go-reader-cli`；cmd/innodb-reader、internal/cli、internal/mysql57、internal/visual、应用测试及固定输入子集 |

核心使用标准库和既有io.ReaderAt/context/预算模型，库不导入应用；CLI通过公开API依赖库，MySQL驱动只归CLI。rowio/stats作为可复用辅助包保留，统计表格便利接口不在本次裁剪；不增加解析子包、对象门面或第三个项目。

单仓单模块不能隔离发布与模块依赖，将rowio/stats一并迁出会增加公共协议迁移，所以采用上述最小边界。原始快照、SQL及验证报告继续归库，CLI复制实际测试和用法所需的固定子集，用testdata/SOURCE.json及检查器核对来源和SHA256。两端不依赖共享测试服务或运行时数据库。

## 导入与兼容

旧本地导入名innodb-go-reader改为正式模块路径；根包名仍为innodb，rowio/stats沿用子包名。旧公开visual包迁为CLI内部实现，不保留反向依赖或转发层。物理格式、列类型、错误链、精确值、Complete/Passed及输出预算没有因拆分改变；CLI二进制和十个命令保持原名。

CLI使用指南及构建脚本在[CLI项目](https://github.com/kimihiro-dev/innodb-go-reader-cli)，库保留权威支持矩阵及现有手册编号。手册中的应用命令在CLI仓库执行，库调用示例在本仓库执行；原始验证材料中的历史构建路径只描述当时来源。

## 本地联合开发

将两个Git仓库放在同一父目录，各自管理分支和提交。当前父目录还有其他项目，因此go.work放在CLI根目录并忽略提交，避免改变其他项目的Go环境。在CLI根目录首次执行：

```sh
go work init . ../innodb-go-reader
go build -o /tmp/innodb-reader ./cmd/innodb-reader
go test -race -timeout=25m ./...
```

已有工作区使用go work use补充模块。工作区直接使用本地库，包含未提交代码；库本身仍可在其目录用GOWORK=off独立验证。编辑器可同时打开两仓目录，并为CLI使用其本地go.work。

CLI的go.mod/go.sum记录实际库版本；通过go get选择已推送的库提交时，Go会生成对应伪版本，无需额外创建标签。工作区直接使用本地代码，关闭工作区后仍须按模块记录的真实版本通过测试和构建；不提交本地replace。工作区通过不能替代真实依赖验证。

## 跨仓工作顺序

- 只改参数、输出和HTML时修改CLI；只改解析时修改库，并按需升级CLI依赖交付修复。
- 跨仓功能先在关联分支完成库和CLI适配，在工作区联调；再合并库、让提交可被取得，更新CLI依赖并独立验证，最后合并CLI。
- 破坏性API调整优先通过兼容新增入口完成CLI迁移，再处理旧入口，避免要求两仓同时合并。库新增能力不自动表示CLI已有对应参数。
- 库维护物理语义、公共契约及独立证据；CLI维护命令用法、文件发布和应用回归。共享固定输入按来源及摘要同步。
- 两仓版本独立，不要求同步标签或每次改动创建Release；开发跟随各自最新提交，交付记录实际CLI提交与实际库依赖。

远程仓库创建、推送、标签和发布另按用户授权执行。双快照比较、独立可视化项目及新增物理格式均不在本次拆分范围。
