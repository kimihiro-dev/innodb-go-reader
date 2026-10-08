# 74. 投影、回表与验收

接续[第73章](73-secondary-query-navigation.md)。独立投影结果允许读取所需列，同时保留当前页的结构验证；未选择的大字段不会仅因所在行命中就自动读取。

## 两阶段物化与真实外部引用

回表复用聚簇点查，内部先保留尚未解析的外部引用，先取得索引核对/谓词需要的值，再对命中行取得投影列。这个中间 Record 不公开；原 Read/Query 仍返回完整值。页内字段照常解码并校验；INSTANT 默认补全、STORED、不可见物化列和隐藏 ROW_ID 使用既有映射。

新 `huge` 快照有两行。id=1、n=1、marker=17、payload 长16777217字节，比现有16MiB类型物化上限多1。二级页5、origin125的8字节为：

```text
80 00 00 01 | 80 00 00 01
 signed n=1| signed id=1
```

查询 n=1、投影 marker，聚簇结果为页4、origin128。外部引用在 origin之后25字节（id4、事务字段13、n4、marker4），绝对偏移65689，实际20字节：

```text
0000003a | 00000006 | 00000001 | 0000000001000001
space=58 | first=6  | offset=1 | flags=0,length=16777217
```

此处 offset=1 是当前新式 LOB 引用含义，不能按普通页内字节偏移解读。显式 schema 查询仅物理读取 `[0,5,4]`，未读取 LOB_FIRST 页6，返回17。投影 payload 或完整 Read 仍明确 ErrUnsupported，类型上限没有放宽。

另一损坏测试修改前缀表 LOB_FIRST 页内容且不重算 CRC：不命中的完整原值查询即使投影 payload 也成功零行；真正命中且选择 payload 时触发 CRC 错误。前缀候选与精确命中的区分决定是否访问 LOB。

## 共享预算与报告

元数据发现、二级导航、每次聚簇点查和实际 LOB 读取共用一个 scanReader、LRU、context和累计预算。每次回表不能重置预算。默认 MaxEntries=1000000；3000行逐次回表会重复验证已缓存聚簇页上的记录，因此可能达到该累计工作量上限。完整 SQL 矩阵显式设10000000，既不更改默认值，也不把缓存命中当作没有遍历工作。

MaxRows只计交付给回调的最终行，在读取投影 LOB 前检查；MaxRowBytes包含候选二级/聚簇本地源字节、默认值源字节和实际解引用的外部数据。单值类型物化上限仍独立生效。Limit只计成功回调，满足后 Complete=true、LimitReached=true；取消、I/O、预算和回调错误保留已交付结果的统计并返回错误，Complete=false。ErrStopped仍表示调用方停止。

| 报告项 | 含义 |
|---|---|
| SecondaryPages / ClusteredPages | 二级与聚簇遍历的页访问计数，聚簇页可因多次点查重复 |
| Candidates / Filtered | 导航得到的当前候选及完整谓词排除数 |
| Lookups / Covered | 发起回表次数及以覆盖方式交付的行数 |
| Records / DeletedRecords | 交付行数及遍历识别的删除标记数 |
| PageReads / CacheHits / PhysicalReads | 共享 reader 请求、缓存命中和实际底层读取 |
| TraversalEntries | 各条路径累计的结构遍历工作量 |

计入回调的行即使回调报错也进入 Records；它不代表成功消费。前缀样例56候选、45过滤、56回表、11交付，说明 Lookups 可以大于 Records；存在完整索引副本时则可提前过滤，见73章的120→1例子。

## 真实资产与独立预期

新增资产位于[testdata/secondary_query](../../testdata/secondary_query/README.md)，使用 MySQL8.0.45、16KiB、crc32、独立表空间，隔离临时库 `innodb_reader_secondary_query_bdd66c509cc1`，实例采集后已正常关闭。

| 快照 | 当前表行 | SQL查询 | 覆盖重点 |
|---|---:|---:|---|
| prefixes | 480 | 270 | name(3)、rank DESC、NULL、空串、多字节/NUL、碰撞、LOB |
| covering | 3000 | 76 | COMPACT、可空重复二级键、超过2^53的BIGINT主键、覆盖/回表 |
| huge | 2 | 12 | 未选超限LOB可查询，选中明确拒绝 |

358条保存的独立 SQL 查询共27340结果行，自动/显式入口和示例输出完全一致。SQL谓词按完整原值比较，以 NULL安全等值及明确的复合方向展开；ORDER BY 按实际前缀物理顺序及定位后缀构造，不拿完整 name 排序替代。验证累计7302次覆盖交付、24640次回表（查询间有重复，不是独立表行数）。三文件官方 innochecksum 严格CRC32、六个SDI对象与官方 ibd2sdi/Go CLI一致，结果保存于 verification.json。

既有第34阶段12快照20索引新增342次查询对照，包含隐藏ROW_ID、字符/二进制前缀、各类标量、INSTANT、STORED、VIRTUAL显式入口及删除项。原始资产未修改。全项目累计487快照：484份在对应完整/仅物化入口恢复98811行，3份完整读取拒绝；huge新增资产虽可投影，仍归完整读取拒绝。

错误/控制测试覆盖缺失聚簇行、索引不一致、覆盖不访问损坏聚簇树、访问/未访问路径损坏、重复/非法列、输入和输出字节所有权、停止/取消/I/O、页/行/字节/遍历预算。深树点查3个二级页对比整树182页。全量race/coverage通过（822.508秒），核心包覆盖率93.7%，vet通过。使用25分钟测试超时：原默认10分钟在SQL矩阵期间耗尽，未报告断言失败或数据竞争。十秒预算模糊测试 FuzzSecondaryQueryRange 完成474802次、FuzzSecondaryPrefixRanges完成8713次；后者用独立完整字符串/可空DESC元组判定，验证候选放宽不会漏行。

## 复跑

```sh
GOCACHE=/private/tmp/innodb-go-reader-stage30-cache go test -race -cover -timeout=25m ./...
GOCACHE=/private/tmp/innodb-go-reader-stage30-cache go vet ./...
python3 scripts/verify_secondary_query_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
```

生成器为[scripts/generate_secondary_query_fixtures.py](../../scripts/generate_secondary_query_fixtures.py)，新生成必须使用新库及输出目录，不能覆盖既有快照。运行帮助获取连接参数；保存的SQL和writer脚本可检查原始查询来源。

[secondaryquery示例](../../examples/secondaryquery/main.go)接收解压后的ibd、索引名和查询JSON：

```sh
go run ./examples/secondaryquery -max-entries=10000000 table.ibd s query.json
```

查询JSON形如 `{"Range":{"Lower":{"Key":[1],"Inclusive":true},"Upper":{"Key":[1],"Inclusive":true}},"Columns":["id","marker"]}`。整数使用 UseNumber 保留精度；二进制键可写 `{"base64":"YWI="}`。`-materialized` 显式选择仅物化入口。输出逐行 row，末尾为 report 或 error；错误退出非零，已输出的行不等于完整成功。

支持范围继续限定现有普通二级索引、类型/字符集和稳定输入文件；不提供SQL执行器、VIRTUAL求值或MVCC恢复。
