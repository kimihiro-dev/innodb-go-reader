# 62 INSTANT：NULL位图、重建与真实验收

学习目标：解释为什么非叶子记录不能使用当前列数计算NULL位图，理解重建如何结束历史布局，并用独立SQL与物理证据验收。

前置：[61 行版本与列映射](61-instant-row-layout.md)、[07 非叶子记录](07-node-pointers.md)。本阶段的官方格式依据来自本地MySQL8.0.45源码。

## 1. 叶子和非叶子的NULL位图不同

叶子先读取行版本，再统计该版本实际存在的可空字段。被后来DROP的原始列在旧行中仍计数；尚未ADD的列在旧行中不计数。

非叶子没有行版本字节；聚簇导航记录虽然只存键和子页指针，却仍预留初始布局的NULL位图。源码 `storage/innobase/rem/rec.cc:70–76` 调用 `get_nullable_before_instant_add_drop()`，`storage/innobase/include/dict0mem.h:1256–1265` 在原生行版本表中取版本0的可空列数。

真实 `tree`：

| 阶段 | 可空列情况 | 新写叶子NULL字节数 | 非叶子NULL字节数 |
|---|---|---:|---:|
| before | 初始n0..n7共8列 | 1 | 1 |
| add | 新增m0..m8，共17列 | 3 | 1 |
| drop | DROP初始8列，当前9列 | 2 | 1 |
| rebuilt | 以当前9列重建 | 2 | 2 |

旧叶子仍按各自版本计算，而不是统一使用表中“新写叶子”的长度。

`tree_drop` 的首个非叶子记录位于根页4，Start120、origin126、End134，origin前真实字节：

```text
00 | 10 00 11 00 0e
NULL  固定头：MIN_REC，其后是堆号/类型/next
```

这里只有一字节NULL位图，不能因为当前有9个可空列就往前多读一字节。本阶段树从800行69个聚簇页增长到1001行85页，根level=1；包含INSTANT之后插入引起的持续树增长，不仅验证最初已有节点。

## 2. 字典默认也需要完整类型解码

`types_defaults` 先插入仅含id的旧行，再一次ADD12列并插入新行。旧行通过DefaultColumns标明字典来源，新行读取物理字节；两条路径应得到相同类型和精确值。

以下hex来自交付SDI，不是手工编造的示意编码：

| 列类型 | ADD默认字节 | 还原值或含义 |
|---|---|---|
| BIGINT UNSIGNED | `ffffffffffffffff` | 18446744073709551615 |
| DECIMAL(20,3) | `7f439eb1ca484078fc85` | -12345678901234567.890，保持scale |
| CHAR(8) utf8mb4 | `e7958cf09f988020` | `界😀`及一个残留ASCII空格 |
| VARCHAR(20) | 零字节，default属性存在 | 非NULL空串 |
| BINARY(4) | `00ff0000` | 保留尾部零，不当成字符串 |
| DATETIME(6) | `99b2bac8b801e240` | 2024-02-29 12:34:56.123456 |
| TIME(3) | `7f3747fb32` | -12:34:56.123 |
| ENUM | `03` | 第三个成员“界” |
| SET | `05` | 第0和2位，标签a,界 |
| BIT(64) | `ffffffffffffffff` | uint64最大值 |
| JSON/BLOB | default_null=1 | SQL NULL，不是JSON null或空BLOB |

CHAR默认采用InnoDB原字段编码，不能用“声明8字符”简单复制8个空格或按8个Unicode码点截断。复用既有字符与类型解码可以让物理字段、页外字段和字典默认遵守相同返回契约。

## 3. 64与重建

`versions_v64` 做32轮ADD/DROP，每轮中间插入一行，最后在第64次布局变更后再插入id100。共有34行：最初无版本字节的行、版本1/3/.../63的行及版本64的行。

版本64记录位于页4，Start1038、origin1044、End1061，开头是：

```text
40 | 40 01 18 fc 5c | 80 00 00 64 ...
v64   固定头0x40      id100
```

两个相邻的0x40有不同含义：前一个是版本数64，后一个是记录头的VERSION标志。当前只剩非NULL的id，因此该记录没有NULL位图。旧行里已经被DROP的v仍有物理字节，默认值和当前Values里都不应出现v。

`ALTER TABLE ... FORCE, ALGORITHM=INPLACE` 实际重建数据。lesson、tree、hidden、versions四组的重建快照中，SDI历史列和Instant布局消失，记录没有版本标志，默认值已物化到新行，解析结果的RowVersion为nil、DefaultColumns为空。重建不是把旧记录的版本字节简单清零：其NULL位图、长度和字段位置都重新组织了。

本阶段非重建验收只覆盖明确的INSTANT ADD/DROP和ALTER DEFAULT，重建验收覆盖上述FORCE/INPLACE；不由这些样本外推任意ALTER算法、类型变更或升级迁移。

## 4. 17份真实快照

来源：独立库 `innodb_reader_instant_e85d0f225972`，资产位于 `testdata/instant/`。

| 表 | 阶段 | 快照数 | 累计SQL行数 |
|---|---|---:|---:|
| lesson | before/add/default_changed/drop/readd/updated/rebuilt | 7 | 30 |
| tree | before/add/drop/rebuilt | 4 | 3802 |
| hidden | add/drop/rebuilt | 3 | 14 |
| types | defaults | 1 | 2 |
| versions | v64/rebuilt | 2 | 68 |
| 合计 | 按快照计数，不是不同逻辑行数 | 17 | 3916 |

生成器 `scripts/generate_instant_fixtures.py` 只在新库建表，设置会话字符/时间/主键相关变量；不改全局、既有用户表或服务状态。每次DDL/DML完成后FOR EXPORT，锁内复制物理文件和导出预期，然后UNLOCK。生成SQL完整保存在writer.sql.gz，密码只由环境变量提供。

每份快照包括：原ibd.gz及SHA256、当前DDL、SQL JSON_ARRAY结果、SQL索引身份、官方ibd2sdi结果，以及显式schema。

**证据独立性**：显式schema的类型声明来自生成器；生命周期、物理位置和默认字节通过官方SDI用独立Python序列化。它不是第二份独立的物理元数据来源。真正独立的值验收来自SQL；另有已知版本/记录字节、默认来源和恶意元数据测试，不能仅靠“自动与手工schema相同”证明格式正确。

## 5. 测试如何定位错误

`instant_test.go` 默认离线执行：

- 对照SHA、官方完整SDI、自动与显式schema、完整Read结果及SQL当前列值；隐藏键表比较多重集合，保留重复行次数。
- 逐记录核对版本字节来源和DefaultColumns，断言77/99的历史默认区别、同名重增、非叶子树、隐藏身份和真实版本64；重建后无Instant元信息。
- 损坏schema的版本、位置、列映射、生命周期、DROP定义/类型、默认NULL/宽度/长度，要求公开Read没有部分返回。
- 损坏记录的零/未来/65版本、缺失标志、旧式0x80及双标志。结构测试重算CRC，避免只碰到校验和错误。
- 损坏SDI的physical_pos、版本、default hex、互斥default/default_null及未知属性，要求InspectTable报错或无可用schema。
- DROP列的旧LOB不追读；合成delete-mark版本行仍保留准确本地Raw，不把合成测试称为真实删除历史验收。
- FuzzInstantRecord直接进入叶子记录解析，覆盖混合版本、NULL长度与字段边界。

官方工具验证17份文件严格CRC32、34个SDI对象以及3916行CLI/SQL结果。旧394份资产保持原样；新增后累计411份，其中410份成功读取78512行，另1份保持原超限拒绝。

最终全量 `go test -race -cover ./...` 通过，核心覆盖率94.1%；vet通过；最终夹具的10秒FuzzInstantRecord执行362933次，无失败。

离线复现：

```sh
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzInstantRecord$' -fuzztime=10s -parallel=2
python3 scripts/verify_key_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin --fixtures testdata/instant
```

重新采集使用 `generate_instant_fixtures.py --mysql ... --socket ... --out 新目录`，凭证为环境变量MYSQL_PWD。页号、事务号、table/space/index ID和SHA会随实例状态改变；本章数值只属于交付夹具。

## 6. 当前边界与下一阶段

旧式INSTANT列数格式使用0x80，后面是字段数的一/两字节编码；当前0x40后面是一字节行版本。二者不是可互换标志。本阶段明确拒绝旧式instant_col及升级混合布局，也拒绝原生布局中显式版本0（该表示涉及升级语义），不在缺少旧版本真实样本时猜测兼容性。

不支持缺失历史物理位置/字段类型/所需ADD默认的读取，不用当前列默认填补未知值。未实现生成列求值、不可见列扩展、事务历史和故障恢复。被DROP字段只为定位而解析布局，不作为“删除数据恢复”输出。

下一阶段是第30阶段生成列与不可见列，需要分别确定STORED物理值与VIRTUAL表达式的返回边界，不能因为SDI列可识别就声称表达式已经求值。
