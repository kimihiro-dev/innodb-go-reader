# 60 JSON更新空洞与LOB真实验收

学习目标：理解JSON部分修改为什么能保持总长度，却改变逻辑内容；区分容器内部空洞、LOB历史块和聚簇页garbage。

前置：[45 二进制JSON](45-binary-json.md)、[59 更新后的LOB](59-updated-lob-layout.md)。JSON返回值继续使用已确认的 `JSONValue` 类型树，SQL NULL为nil，JSON null是节点。

## 1. 长度不变，内容可以变短

二进制JSON容器中的偏移相对**去掉类型标签后的容器起点**，多字节数字是小端。这与LOB引用的大端数字不同。

原解析器要求键和值连续排列，且最后一个值恰好结束于容器末尾。部分更新后这些假设不成立：替换短值留下旧字节，删除成员可以只移动索引项、减少成员数而保留载荷。不能再通过“读完上一个值紧接着读下一个值”恢复内容。

源码依据为8.0.45 `sql-common/json_binary.cc`：`has_space`（1410起）、`update_in_shadow`（1727起）、`remove_in_shadow`（1914起）。尤其1914–1963在删除成员时移动键/值项并更新成员数，没有把后面所有载荷紧缩。

```text
容器头 + 键项/值项 -> key、value实际偏移
                      | 有效值 | 未引用旧字节 | 有效值 | 尾部空洞 |
```

`jsonBinaryDecoder.container` 按每个项的偏移解析子值，记录实际占用区间；排序后检查头、键、值之间不能重叠。空洞和重排允许，键的长度/字节排序、唯一性、UTF-8、容器边界、深度100和100000节点限制继续验证。嵌套容器的整个声明区间属于它，不能让兄弟值借用它内部的空洞。

这些字节是JSON容器内的未引用空间，不是聚簇记录free链，也不是LOB历史版本项；三个层级必须分开解释。

## 2. 页内JSON的真实155字节例子

`documents_before` 的id3没有页外引用。其JSON开头的真实字节是：

```text
00 0300 9a00
1900 0100 1a00 0100 1b00 0100
0c 1c00 0c 4500 02 6e00
61 62 63
28 71 71 71 ...
```

第一个00表示小对象；下面所有偏移相对类型标签后的容器起点：

| 区间或偏移 | 解码 |
|---|---|
| 0–3 | count=3，size=154（0x009a），加外层tag共155字节 |
| 4–15 | 三个键项，键a/b/c分别在25/26/27，各1字节 |
| 16–24 | 三个值项，字符串a在28，字符串b在69，小数组c在110 |
| 28–68 | a：长度字节0x28=40，接40个q |
| 69–109 | b：长度40，接40个r |
| 110–153 | c：44字节小数组，保存两个字符串 |

`documents_holes` 将a改为 `s`，嵌套c[0]改为 `x`：

```text
a原载荷开头：28 71 71 71 ...
a新载荷开头：01 73 71 71 ...
c[0]原开头：12 6c 6f 6e 67 ...
c[0]新开头：01 78 6f 6e 67 ...
```

a的40字节字符串缩为1字节，留下39字节；c[0]原18字节缩为1字节，留下17字节，总空洞56。SQL独立返回 `JSON_STORAGE_SIZE=155, JSON_STORAGE_FREE=56`。剩下的q和旧字符串尾部不能成为额外JSON值。

`documents_reuse` 把a改成20个z、b改成35个v，容器仍155字节，空洞变为42：a余20、b余5、嵌套值余17。不是长度字段错误，也不应该把残留内容当成用户数据。

页外id1同样有空洞：完整LOB始终63865字节；holes阶段SQL空闲60811字节，reuse阶段空闲43810字节。读取时应先完整还原63865字节，再依JSON内部偏移还原逻辑类型树。

## 3. 交付的真实快照矩阵

资产位于 `testdata/lob_updates/`，来源库 `innodb_reader_lob_updates_5d9c5681a7cc`，MySQL8.0.45。共17快照、54行；同一行出现在多份快照中，所以不是54个不同逻辑实体。

| 表 | 快照阶段 | 行数 | 覆盖 |
|---|---|---:|---|
| values | before、inherited、replace、shrink、regrow | 各4 | 主键迁移继承、TEXT/BLOB全量替换、增长缩短、NULL/空值、UTF-8跨块 |
| values | deleted、purged | 各3 | 删除LOB行的摘要与purge后结果 |
| documents | before、small、large、repeated | 各3 | 初始、小修改不增版本、首块迁移、多轮历史链及opaque类型变化 |
| documents | holes、reuse、history_purged、grow | 各3 | 页内/页外空洞、重用、历史链清理后保留分配页、整值增长 |
| documents | deleted、purged | 各2 | JSON删除引用不追读与purge |

`generate_lob_update_fixtures.py` 创建全新独立库，两会话采集：写事务提交后 `FLUSH TABLES ... FOR EXPORT`，持锁复制文件并导出SQL预期，再解锁。另一会话先建立一致性读视图但不访问目标表，用来保留历史，避免目标表MDL阻止导出。中途释放视图获取history_purged，再建立新视图覆盖后续删除。未更改全局配置或原用户表，也未停启实例。

生成器的短暂等待只是给purge运行机会，不是purge完成证明；离线测试独立断言history_purged的活动项历史数为0，删除后的purged没有删除摘要。生成失败会保留失败快照用于诊断，不将其计入已完成验收。

文件说明：

- `manifest.json`：版本/配置、原文件SHA256、每阶段行数/空间索引身份、SQL JSON存储长度/空闲量。
- `*.ibd.gz`、`*.json`：原物理文件与独立手工schema。
- `*.expected.json.gz`：直接保留SQL JSON_ARRAY文本，禁止通过float64中转DECIMAL或大整数。
- `*.indexes.json.gz`、`*.sdi.json.gz`：SQL索引身份及官方ibd2sdi输出。
- `*.sql`、`writer.sql.gz`、`holder.sql.gz`：建表定义和完整实际采集语句，不含密码。
- `verification.json`：官方CRC/SDI和CLI与SQL对照结果。

## 4. 如何验证实现没有“碰巧读对”

`lob_update_test.go` 对每份文件校验SHA、官方SDI、自动/手工schema和完整Read结果。TEXT按文本与原字节来源对照，BLOB转HEX，JSON的普通视图按精确有理数比较SQL，并检查从Chunks重新拼接的数据可还原相同类型树。旧JSON测试继续覆盖具体opaque/DECIMAL类型。

夹具还必须满足拓扑断言：small使用相同引用版本和块来源；large确实换块；存在真实inherited标志和跨块UTF-8；至少两个额外索引页、13项历史链；history_purged的当前Record与此前一致且历史数确实为0。

损坏测试只改内存副本并重算页CRC，使错误进入结构检查：旧/零/未来引用版本、历史数量/循环/prev/尾地址、历史版本倒序/零版本、嵌套历史、活动/历史槽复用、历史页号/长度、分配页循环等。公开API失败必须返回nil。JSON另测重排载荷、空洞、空键、尾部空洞，以及键/值重叠和越界。

旧夹具保持原样，历史“修改即拒绝”测试改为当前契约：允许的生命周期标志有成功测试，未知标志和修改进行中仍拒绝。不能把历史阶段的拒绝标记当作永久格式定义。

离线复现：

```sh
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzUpdatedLOB$' -fuzztime=10s -parallel=2
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzJSON$' -fuzztime=10s -parallel=2
python3 scripts/verify_key_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin --fixtures testdata/lob_updates
```

重新采集使用生成器的 `--mysql`、`--socket`、`--out` 参数，out必须不存在，凭证仅通过环境变量MYSQL_PWD传入。复跑会产生新数据库，事务号/页号/SHA可能不同，不应硬套本章样本数字。

实测结果：17文件官方严格CRC32、34个SDI对象和54行CLI/SQL全部通过。完整 `go test -race -cover ./...` 通过（核心94.2%），vet通过；10秒预算FuzzUpdatedLOB执行31980次、FuzzJSON执行700008次，无失败。累计394份资产中393份成功读取74596行，另1份保持超限拒绝。

## 5. 边界

本阶段读取当前存储值，不做事务提交判定、undo回放、历史任意时刻读取、旧格式/压缩/加密LOB。delete-mark仍只有本地摘要，不恢复删除值。历史数据页和未访问空间不属于本次CRC检查范围；读当前值成功不等于全文件所有旧页都健康。

后续第29阶段是INSTANT ADD/DROP COLUMN和行布局版本，须单独解释物理列映射与历史默认值；不能从LOB版本推导行布局版本。
