# 58：受控变化快照、purge 与树收缩验收

本章解释怎样得到可重复的 UPDATE/DELETE 文件证据，并验证第 57 章的输出语义。**“未带 delete-mark 的物理记录”与“任意事务视图下可见的 SQL 行”不是同一个概念**；本阶段只在目标写事务已提交、文件稳定的受控样本上与当前 SQL 行对照。

## 1. 为什么使用两个会话

如果提交 DELETE 后立即等待 purge，文件里可能已经没有 delete-mark；若只测试这种文件，跳过删除标志的实现也可能看似正确。因此生成器明确采集两个时间点：

```text
写会话：创建新表、插入 → before 快照
读会话：START TRANSACTION WITH CONSISTENT SNAPSHOT
写会话：START TRANSACTION → UPDATE/DELETE → COMMIT
写会话：FOR EXPORT → 当前 SQL 预期 + 文件 → UNLOCK → marked 快照
读会话：COMMIT，释放旧读视图
写会话：有限等待/重新导出 → delete-mark=0 → purged 快照
```

只读会话不访问目标表，避免持有目标表的元数据锁而阻塞导出。它保留旧读视图，让 purge 暂时不能移除仍可能需要的旧版本；目标写事务在采集 marked 前已经结束。

`FOR EXPORT` 提供稳定导出过程，源码 `storage/innobase/row/row0quiesce.cc` 的 purge/quiesce 路径说明其与后台清理的协调。它不会为解析器创造任意 SQL ReadView。生成器自己的“两会话、写事务已提交”步骤是验收输入契约的一部分，不能从一份陌生 ibd 自动证明这些条件成立。

purge 后采集最多尝试 30 次，每次解锁后才等待。没有删除标记的真实样本才能作为 purged 资产；等待超限会失败，不用合成文件冒充真实清理结果。

## 2. 全删后的两种空表

真实 `empty_marked` 的 SQL 行数为零，但物理根页 4 有：

```text
PAGE_N_RECS=20, HeapCount=22, HeapTop=680
PAGE_FREE=0, PAGE_GARBAGE=0
Records=[], DeletedRecords含20条
```

其中首条 `Start=120, Offset=127, End=148`，头 `20 00 10 00 1c`；键字节 `80 00 00 00` 为 INT 0，本地 v 仍是 gone。不能因为普通行数为零就停止校验这条记录链。

`empty_purged` 仍是同一个根页号，却已成为真正的空根叶页：`PAGE_N_RECS=0, HeapCount=2, HeapTop=120`，页目录只保留 infimum/supremum；普通行与删除摘要均为空。这两份文件回答不同的物理问题，不能只比较 SQL COUNT(*) 就认为布局相同。

## 3. 树可以变浅，根页号可以不变

树案例使用 `PRIMARY KEY(k,id DESC)`，k 为实际 255 字节的 VARBINARY，普通 v 最初 1600 字节。写事务删除 id<3990 的记录，并把留下十行的 v 缩短为 32 字节。

| 快照 | 普通行 | delete-mark | 聚簇页数 | 根 level |
|---|---:|---:|---:|---:|
| tree_before | 4000 | 0 | 816 | 2 |
| tree_marked | 10 | 3990 | 815 | 2 |
| tree_purged | 10 | 0 | 1 | 0 |

三个快照根页号都是 4，index ID 都是 883，space ID 都是 659。页号稳定不意味着内容/层级稳定，也不能复用旧遍历结果。marked 时已因行缩短发生部分结构变化；purge 后剩余行合并进根叶页，原非叶子遍历路径不再有效。

最终根页真实值为 `HeapTop=7050, HeapCount=14, Records=10, Free=2014, Garbage=3770`。单页结果仍可能有空闲记录和垃圾，不能把“树变成单页”误当成页内记录已紧密重排。

遍历始终从当前 SDI/根页和当前子指针出发；不把文件里仍留着旧 INDEX 字节的脱离树页面全部当作活动页扫描。`Result.Pages` 是当前聚簇树访问集合，不等于文件全部已分配/曾使用页面的数量。

## 4. 当前行和删除键使用独立预期

每个阶段的 SQL SELECT 在写事务结束后执行，显式/唯一聚簇键按完整键及方向 ORDER BY；隐藏 ROW_ID 表按用户行多重集合比较，保留重复次数。

删除摘要还做额外检查：从已经与 SQL 对照的 before 行构造完整键集合，扣除 marked 中仍存在的当前键，余下的键必须与 DeletedRecords 一一对应。隐藏键使用实际六字节身份。这样不会只拿解析器自己统计的 delete-mark 数字来验证解析器。

本组 SQL 操作没有跨事务反复产生同键历史版本；删除后重插的 id=5 当前键仍存在，实际没有单独删除摘要。这个具体结果不能推广为“每条 DELETE 都必定有一份残留”或“删除摘要数量就是 SQL 影响行数”。

普通行的更新载荷还验证了：127→128 字节长度边界、NULL 与空字符串、文本增长到 1200 字节、记录迁移、隐藏 ROW_ID 下的值变化，以及带二级索引的字符复合 DESC 唯一键变化。

## 5. 为什么删除摘要只保留本地 Raw

删除记录可能引用旧的页外版本。阶段 28 才处理更新后的复杂 LOB；本阶段不能跟随这些引用后把读到的任意字节当作完整历史值。

测试使用一份既有 ROW_ID+BLOB 快照的**内存副本**，只把一条行标记删除，并把仅它引用的 LOB 首页面类型破坏。其他行继续正常还原，删除摘要保存本地引用区域，但读取不访问该 LOB 页。此项是明确标注的合成隔离测试，不是真实旧 LOB 恢复实验。

与此同时，删除记录的键、变长布局和记录占用范围仍参与验证。重复键、错误键范围、过长字段、目录 ownership、混入 INSTANT/版本/MIN 位都必须报错，且不能返回已收集的一部分普通行。保留证据不是放弃物理结构检查。

## 6. 交付矩阵与验证结果

最终独立数据库为 `innodb_reader_changes_a6dcd49fc617`，文件在 `testdata/changes`。

| 表组 | 快照 | 当前 SQL 行数合计 | 保留 delete-mark 数合计 |
|---|---|---:|---:|
| lesson | before/marked/purged/reused | 23 | 2 |
| empty | before/marked/purged | 20 | 20 |
| hidden | before/marked/purged | 8 | 2 |
| unique | before/marked/purged | 10 | 2 |
| tree | before/marked/purged | 4020 | 3990 |

共 **16 快照、4081 行当前值、4016 条删除摘要**。原 361 份资产保持不变；累计 **377 资产、376 成功资产、74542 行，另 1 份既有超限拒绝资产**。这里按快照统计行数，同一逻辑行在不同快照中分别验收。

16 文件全部通过官方 `innochecksum --strict-check=crc32`，32 个 SDI 对象与 `ibd2sdi` 逐对象一致，auto CLI 的所有行与 SQL 预期一致。每份文件校验 SHA256；自动/手工 Schema、完整结果、SQL 字典索引身份、删除 Raw/头/边界、在链记录数等式均通过。

完整 `go test -race -cover ./...`、`go vet ./...` 通过，核心覆盖率 93.9%。10 秒预算 `FuzzChangedPage` 完成 8557 次，无失败；直接变异页内结构以覆盖删除记录解析，不把全部样本挡在外层 CRC。结构损坏测试在内存副本重封 CRC，真实资产从不修改。

## 7. 复现与输出边界

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzChangedPage$' -fuzztime=10s -parallel=2
python3 scripts/verify_key_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin --fixtures testdata/changes
```

解压一份快照后，可运行 `go run ./examples/changes table.ibd` 查看 Rows、DeleteMarked、Pages、FreePages、GarbageBytes 和 RootLevel；`examples/auto` 继续只输出完整普通行。需要删除原始证据时直接调用 Read/ReadAuto 读取 DeletedRecords。

重新生成用 [generate_change_fixtures.py](../../scripts/generate_change_fixtures.py) 的 `--mysql`、`--socket`、`--out 新目录`，密码只通过 MYSQL_PWD 环境提供。`writer.sql.gz` 与 `holder.sql.gz` 分别保存两个会话的实际 SQL，生成期间只操作新独立库并设置会话变量；原实例及全局配置保持不变。

当前不提供任意时点恢复、undo 回放、提交状态判断或 free 残留行恢复。`Records` 的语义是未标记删除的当前物理值，只有输入满足受控稳定快照条件时，才与本阶段的 SQL 当前行预期对应。后续阶段 28 将处理当前行更新后的 LOB 版本与完整值。
