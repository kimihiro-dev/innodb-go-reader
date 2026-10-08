# 56. 六字节 ROW_ID、重复行身份与验收

<a id="56六字节-row_id重复行身份与验收"></a>

本章学习在没有用户聚簇键时定位记录，区分“用户列相同”与“物理行相同”，并验证多层隐藏键树。索引选择和 API 见[第 55 章](55-clustered-identity.md)。

<a id="1-系统键放在-origin而不放进-values"></a>

## 系统键放在 origin，而不放进 Values

隐藏键叶子布局示意：

```text
逆序变长长度 | NULL 位图 | 5 字节头 | ← origin
DB_ROW_ID(6) | DB_TRX_ID(6) | DB_ROLL_PTR(7) | 所有用户列（SQL声明序）
```

ROW_ID 是无符号六字节大端数。它不使用有符号 SQL 整数的最高位翻转，也不按 SDI 中 INT24 枚举当成三字节。Go 无原生 uint48，因此用 uint64 容纳，精确范围为 0..281474976710655。

`dict0boot.ic:36–50` 从 `dict_sys->row_id` 分配并递增计数；55–70 附近用 `mach_read_from_6` / `mach_write_to_6` 读写。该计数不是用户表内从 1 开始的业务 ID，也不能从相邻行号推断其值或要求无间隙。解析器验证键序和范围，不要求连续。

相比用户键布局，所有用户列都留在系统字段之后；长度数组和 NULL 位图只描述用户列。ROW_ID、TRX_ID 和 ROLL_PTR 都固定宽度且不为 NULL，不消费这些元数据。

<a id="2-逐字节读取真实样本"></a>

## 逐字节读取真实样本

`testdata/cluster/rowid_lesson` 定义两个可空用户列：`n INT, text VARCHAR(32)`，无用户索引。以下是最终交付文件页 4 的第一行，所有区间都是**页内偏移，结束位置不包含**：

| 区间 | 实际字节 | 含义 |
|---|---|---|
| 120..121 | `06` | text 为六字节 UTF-8 |
| 121..122 | `00` | n、text 都非 NULL |
| 122..127 | `00 00 10 00 23` | 普通记录头，heap number=2 |
| 127..133 | `00 00 00 00 31 ec` | ROW_ID=12780 |
| 133..139 | `00 00 00 00 8d e1` | 事务 ID 原始字段 |
| 139..146 | `82 00 00 00 d3 01 10` | 回滚指针原始字段 |
| 146..150 | `80 00 00 07` | n=7 |
| 150..156 | `e9 87 8d e5 a4 8d` | text="重复" |

`Start=120, Offset=127, End=156`；文件绝对 origin 是 `4×16384+127=65663`。记录头最后两个字节 `00 23` 表示下一条 origin 的相对位移 35，即下一条 origin=162。

调用结果中：

```text
Record.RowID -> uint64(12780)
Record.Values -> [int32(7), "重复"]
```

第二行 `Start=156, Offset=162, End=181`，只有一个 `03` NULL 字节，两个用户列都为 NULL，所以没有变长长度字节。其载荷只含 ROW_ID=12781 和 13 字节事务字段，共 19 字节。

第三行的用户值又是 `[7,"重复"]`，但 origin=188，ROW_ID 字节是 `00 00 00 00 31 ee`，值为 12782。这是另一条合法行，不应因为 Values 相同就去重。相反，两条活动记录如果拥有同一个完整 ROW_ID，便违反聚簇键唯一性，读行必须失败。

`RowID` 以指针区分“该布局不存在隐藏键”和“该数值为零”。本阶段用合成内存副本覆盖 0、1、2^47、2^48−1；这些是解码边界测试，不声称真实 SQL 实例分配了上述端点。六字节原始证据仍可用 PageNumber/Offset 从快照定位。

<a id="3-非叶子保持相同的六字节键"></a>

## 非叶子保持相同的六字节键

隐藏键非叶子结构：

```text
保留 NULL 位图 | 5 字节头 | ROW_ID(6) | 子页号(4)
```

不包含 TRX_ID/ROLL_PTR，也没有普通用户变长列的长度数组。不过保留 NULL 位图的宽度仍与索引可空用户列数量有关，不能因为键自身非 NULL 就省略。

真实 `rowid_deep` 有 10 个可空用户列，需要两字节保留位图。它的根页 4、level=2，首两条导航记录为：

| Start / origin / End | 位图 | 记录头 | 六字节键 | 子页号 |
|---|---|---|---|---|
| 120 / 127 / 137 | `00 00` | `10 00 11 00 11` | `00 00 00 00 31 f8` = 12792 | `00 00 00 25` = 37 |
| 137 / 144 / 154 | `00 00` | `00 00 19 00 11` | `00 00 00 00 3e a4` = 16036 | `00 00 00 26` = 38 |

第一条带 MIN_REC 标志，继承父任务下界；第二条是有限下界。每条 `End−Offset=10`，公开 `NodePointer.Key` 为 uint64。范围比较直接比较六字节大端编码，不使用显示字符串，也不会按用户列数构造空元组。

这棵真实树共 12000 行、1719 个聚簇页、1718 条导航记录。首叶页 5 第一行 `Start=120, Offset=129, End=2148`，元数据为 `d0 87 03 fe`：`fe 03` 是十列的 NULL 位图（payload 非 NULL，其余九列为 NULL），`87 d0` 逆向组成 payload 的 2000 字节长度。origin 处才开始 ROW_ID；不能把长度数组前移六字节或给系统列分配 NULL 位。

<a id="4-sql-无自然顺序保证比较多重集合"></a>

## SQL 无自然顺序保证，比较多重集合

用户 SQL 的普通行输出没有隐藏系统列，也没有指定排序。测试不会要求其返回顺序等于 ROW_ID 顺序，而是将每个完整用户行规范化后计数，再比较两个多重集合。计数不能退化成集合，否则丢失重复行也可能通过。

`rowid_deep` 的 12000 行用户值完全相同，正好验证“相同值保留 12000 次”。`rowid_lob` 中两条行的完整 BLOB 也相同，且都经页外路径还原；输出比较采用 SQL HEX。

用户 UNIQUE 聚簇键则可以通过完整 `ORDER BY` 独立比较行序：`unique_text_tree` 的字符键和整数成员都是 DESC，600 行、48 个聚簇页，非叶子仍使用阶段 25 的变长键规则。隐藏键与用户键共用父子范围、同层链、重复页和 CRC 检查，不另建扫描引擎。

<a id="5-交付证据与实际边界"></a>

## 交付证据与实际边界

最终独立数据库：`innodb_reader_cluster_4fbb5a8ad9f7`；新资产在 `testdata/cluster`。

| 文件组 | 行数 | 目的 |
|---|---:|---|
| rowid_lesson / rowid_empty | 4 / 0 | 无索引、NULL、重复值及空树 |
| rowid_nullable_unique | 3 | UNIQUE 可空时仍用隐藏键，允许多个 NULL |
| rowid_prefix_unique | 2 | UNIQUE 前缀不能被误当完整聚簇键 |
| rowid_lob | 3 | 两份重复的 60000 字节页外 BLOB |
| rowid_deep | 12000 | 两字节 NULL 位图、真实三层树 |
| unique_single | 3 | 无显式主键，BIGINT UNSIGNED 唯一键及最大值 |
| unique_multiple | 3 | 可空唯一键先声明，实际选择 z_chosen |
| unique_text_tree | 600 | 字符复合 DESC 唯一聚簇键与普通二级索引 |
| primary_secondary | 3 | 原显式主键与二级索引共存 |

共 **10 份新快照、12621 行**。每份保存生成 SQL、DDL、手工 schema、独立 SQL 行预期、SQL 字典全部索引身份、SHA256 以及官方 SDI。字典证据与文件均在同一导出锁期间采集；SQL TYPE 的聚簇位与 `IndexMetadata.Clustered` 对照，隐藏索引同时核对 DD/运行时名称差异。

10 文件通过官方严格 CRC32，20 个 SDI 对象与官方工具完全一致；CLI 输出经有序比较或多重集合比较全部通过。自动与手工 Schema、完整读取结果一致。旧 351 资产保持不改；历史 metadata/added、metadata/recreated 的二级索引拒绝契约更新为聚簇读行成功，新增验证 8 行。合计 **361 资产、360 成功资产、70461 行**，仍保留一份既有超限拒绝资产。

损坏测试覆盖重复/倒序 ROW_ID、错误节点范围、子页号、非零保留位图、截断六字节键/事务字段/子页号，以及系统列宽度、可空/unsigned、隐藏标记、元素顺序、方向和错误索引身份。另验证不能因真实候选不支持而改选另一个唯一索引。错误不返回成功似的部分表。

完整快照必须由调用方保证稳定；ROW_ID 是身份，不是提交状态或事务可见性证明。本阶段不实现二级记录解析、undo、UPDATE/DELETE 残留过滤、FTS/SPATIAL 或列布局变更。下一阶段 27 才开始受控更新、删除后的当前物理记录。

<a id="6-复现与代码入口"></a>

## 复现与代码入口

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzRowID$' -fuzztime=10s -parallel=2
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzHiddenPage$' -fuzztime=10s -parallel=2
python3 scripts/verify_key_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin --fixtures testdata/cluster
```

完整 race/vet 通过，核心覆盖率 93.9%；10 秒预算 FuzzRowID 673916 次、FuzzHiddenPage 19642 次通过。随后补充 ROW_ID unsigned 属性拒绝，并单独重跑其 race 测试。页级 fuzz 直接进入结构解析，避免随机变异全部被外层 CRC 拦截；真实文件验收仍严格检查 CRC。

重新采集用 [generate_cluster_fixtures.py](../../scripts/generate_cluster_fixtures.py) 的 `--mysql`、`--socket`、`--out 新目录`；密码仅通过 MYSQL_PWD 环境提供。实例开启了强制主键，生成器只在自身会话关闭 `sql_require_primary_key` 和 `sql_generate_invisible_primary_key`，避免测试对象被替换为自动生成 BIGINT 主键；全局设置不变。已有表不改，原实例保持运行，早期独立测试库未删除。

代码：[schema.go](../../schema.go) 的 `ClusteredKey`、[record.go](../../record.go) 的 `RowID` 与六字节提取、[key.go](../../key.go) 的 `rowIDValue` / `compareIndexKey`、[tree.go](../../tree.go) 的隐藏导航键路径；完整离线验收见 [cluster_test.go](../../cluster_test.go)。
