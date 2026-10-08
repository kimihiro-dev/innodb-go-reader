# 82. 报告命令、数据核对与交互验收

## 学习目标

生成可离线打开的HTML，理解显式层级和失败语义，并把报告中的数值、边和字节追溯到既有API及固定文件。前置为[第81章](81-visual-report-model.md)。

## 常用命令

在仓库根目录执行：

```sh
go build -o /tmp/innodb-reader ./cmd/innodb-reader
/tmp/innodb-reader visualize --output /tmp/space-report.html testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader visualize --index PRIMARY --pages 0,4 --manual-dir docs/format --output /tmp/lesson-report.html testdata/mysql8045/lesson_rows.ibd
```

第一条报告只有严格空间层；第二条完整扫描PRIMARY，并嵌入页0和4的字节、页4的记录来源。`--pages`是最多256个不重复页号的逗号列表，不能指定范围字符串。先用metadata查准确索引名（隐藏聚簇名称可能为GEN_CLUST_INDEX），用报告页信息确定需要嵌入的页。

二级树与LOB样本：

```sh
gzip -dc testdata/secondary/deep.ibd.gz > /tmp/visual-secondary.ibd
/tmp/innodb-reader visualize --index b_idx --pages 0,5,313 --output /tmp/secondary-report.html /tmp/visual-secondary.ibd
gzip -dc testdata/variable/external_text.ibd.gz > /tmp/visual-lob.ibd
/tmp/innodb-reader visualize --index PRIMARY --pages 0,4,5,6 --output /tmp/lob-report.html /tmp/visual-lob.ibd
```

所选索引仍完整读取；`--pages 313`不会把整树验证缩成一个叶页。含VIRTUAL的聚簇视图必须显式`--materialized`；二级索引不接受这个标志，也不自动回表求虚拟值。空间层不依赖用户行schema，可以处理本次空间能力支持的单个分区文件；单文件索引层保留原分区拒绝契约。本版不接受`--manifest`。

`--output`成功后原子发布，默认不覆盖；确需替换可加`--overwrite`。参数/清单式页号语法错误退出2，损坏/不支持/预算/IO错误退出1，取消退出130。报告任一请求层失败，不向stdout写HTML；渲染中写错误仍可能有stdout前缀。输入文件受输出身份保护，失败不会覆盖既有目标。

预算标志：空间`--max-pages`、`--space-max-entries`；源读取`--max-read-calls`；显式索引`--max-page-reads`、`--max-entries`、`--max-rows`、`--max-row-bytes`、`--cache-pages`；报告`--max-details`、`--max-report-bytes`。未选索引时显式扫描预算参数会报使用错误。详细默认值见`visualize --help`和第81章。

## 已生成的示例

可在本地浏览器打开：

- [lesson.html](../../examples/visual/lesson.html)：7页、4条记录，适合核对记录高亮和页布局。
- [lob.html](../../examples/visual/lob.html)：9页、2条记录，嵌入0/4/5/6页，适合检查引用和数据块。
- [secondary.html](../../examples/visual/secondary.html)：960页表空间、182页二级树、181条父子边，嵌入0/5/313页及10条选定记录详情。

这三份报告只含固定测试夹具；生成的报告会包含所选原始页字节，因此用户数据报告应按原始文件的数据敏感性保管。示例没有额外加载资源，脱离仓库也能查看；手册链接需指定本地目录或按章节路径查找。

## 独立数据验证

[verify_visual_reports.py](../../scripts/verify_visual_reports.py)构建正式CLI，生成6类报告，对照独立space命令结构化输出、原文件字节、记录/LOB引用偏移，并用Node检查JavaScript语法。预期不从HTML图形反推，也不将JavaScript语法通过称为浏览器交互通过。

```sh
python3 scripts/verify_visual_reports.py --out /tmp/new-visual-reports
```

输出目录必须是新目录；运行需要Go及Node，产品本身只依赖Go标准库，打开HTML不需要Node。

| 场景 | 文件页数 | 选定树页 / 边 | 记录详情 | 已嵌入页 |
|---|---:|---|---:|---|
| lesson | 7 | 1 / 0 | 4 | 0、4 |
| external_text LOB | 9 | 1 / 0 | 2 | 0、4、5、6 |
| deep b_idx | 960 | 182 / 181 | 10 | 0、5、313 |
| mixed_instant 物化视图 | 15 | 1 / 0 | 4 | 0、4、5、6、11、12 |
| grown空间 | 17408 | 未请求 | 0 | 0 |
| ranges-p0空间 | 29 | 未请求 | 0 | 0 |

6份报告的Space字段与空间API一致（未知页Raw按规则不自动嵌入）；原始页十六进制与源文件逐字节相等，LOB Reference与记录中20字节一致，嵌入JSON可独立解析，所有整数保持字符串。[验证记录](../../examples/visual/verification.json)注明自动验证范围；用户验收记录见本章末尾。

Go单元测试还逐项对照公开Read/ReadMaterializedAuto/ReadSecondaryAuto的TreePages、Edges和所选记录/LOB来源。覆盖多层树、二级删除记录、VIRTUAL严格/物化选择、分区空间成功但单文件索引失败，以及17,408页空间样本。没有为可视化重新采集MySQL数据，也没有改旧夹具。

错误测试涵盖缺索引、重复/越界页、损坏页、短读/短写、取消、各层预算和精确读取阈值、输入身份保护、失败覆盖回滚。HTML测试用`</script>`恶意名称、uint64最大值、本地路径空格/#和fuzz输入验证转义与精度。

```sh
go test -race -cover ./visual ./internal/cli
go test ./visual -run '^$' -fuzz '^FuzzHTMLReport$' -fuzztime=10s -parallel=2
go vet ./...
go test -race -cover -timeout=25m ./...
```

2026-09-30最终结果：完整`go test -race -cover -timeout=25m ./...`通过，核心857.153秒/92.5%，rowio149.512秒/89.7%，CLI9.786秒/89.2%，visual缓存通过/91.4%（最终专项实际运行10.373秒通过）。`go vet ./...`通过，最终HTML fuzz十秒预算执行32808次无失败。三份交付示例与最终CLI重新生成的HTML逐字节一致。此前中断的长回归不计为通过。

## 浏览器交互验收状态

自动数据/编码/脚本语法测试已通过。用户收到二级树与LOB示例后明确回复“已检查，交互正常”，完成页号定位、父子页点击、记录高亮和LOB块导航的本地浏览器验收。Codex内置浏览器拒绝file://协议，并明确禁止用替代浏览器表面或间接路径绕过。没有启动临时HTTP服务或绕过该策略，也没有将未看过的页面称为视觉验收通过。

用户确认与自动验证分开记录，不声称自动完成截图/跨浏览器/窄屏测试。后续可按页号与分配过滤、树/同层按钮、记录/字节翻页、LOB路径、未嵌入提示及手册链接复跑交互检查。
