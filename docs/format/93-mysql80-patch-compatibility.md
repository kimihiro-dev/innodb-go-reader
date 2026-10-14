# 93. MySQL 8.0：SDI版本与旧式INSTANT兼容

## 学习目标与前置概念

区分发行补丁、DD版本、SDI序列化版本和逐行布局，理解为何同为8.0的文件仍需不同元数据解释。先阅读[SDI提取](41-sdi-records.md)、[SDI转Schema](43-sdi-schema.md)、[紧凑行](65-compact-row-layout.md)、[现代INSTANT](61-instant-row-layout.md)与[当前LOB](59-updated-lob-layout.md)。本章说明2026-10-14当前源码的扩展，固定v0.1.0仍保持原8.0.45范围。

## 准确版本组合

自动解释只接纳以下组合，不将未测试的中间补丁自动纳入。文件中的SDI是序列化时的版本标记，不证明当前在线服务器版本，也不证明事务一致性。此次使用各准确版本新初始化的独立实例；跨版本升级后混合DD/SDI或混合INSTANT文件未验收。

| MySQL | mysqld_version_id | dd_version | sdi_version | 本轮INSTANT |
|---|---|---|---|---|
| 8.0.11 | 80011 | 80011 | 1 | 未提供INSTANT ADD能力 |
| 8.0.12 | 80012 | 80012 | 1 | 旧式末尾追加 |
| 8.0.16 | 80016 | 80016 | 80016 | 旧式末尾追加 |
| 8.0.19 | 80019 | 80017 | 80019 | 旧式末尾追加 |
| 8.0.23 | 80023 | 80023 | 80019 | 旧式末尾追加，INVISIBLE |
| 8.0.28 | 80028 | 80023 | 80019 | 旧式末尾追加，INVISIBLE |
| 8.0.30 | 80030 | 80023 | 80019 | 现代行版本，ADD/DROP及GIPK |
| 8.0.45 | 80045 | 80023 | 80019 | 既有完整基准与回归 |

准确源码版本、相关文件SHA256和定义行号保存在[源码核查](../mysql80-patch-validation/source-versions.json)。其中8.0.11/12的常量名为`sdi_version`；后续改为`SDI_VERSION`。8.0.23以后源码发行版本文件名为`MYSQL_VERSION`，不能因找不到`VERSION`就认为源码缺失。

## 元数据的早期表示

8.0.11/12的SDI v1不序列化索引字段的`hidden`，但索引对象和列本身仍含各自的隐藏属性。普通InnoDB BTREE中，声明键字段有具体长度，内部追加字段用`length=4294967295`；系统列或隐藏聚簇索引也提供身份依据。因此当前代码仅在这两个准确版本按这些规则恢复字段属性。后续版本仍要求显式`hidden`，缺失不能被默认成可见字段。

早期DD的BLOB/JSON/geometry `char_length=8`描述SQL字段对象的内部表示，不是8字节载荷限制。归一化只用于早期版本且原长度为8的受支持类型，输出报告仍保留原DD长度。解码参数按类型确定容量：TINY 255、普通65535、MEDIUM 16777215、LONG/JSON/geometry 4294967295；物化仍受16 MiB实现上限约束。两早期版本正常文件还可缺省表/空间的加密和状态属性；页零的压缩/加密标志继续严格验证，不因缺省属性接纳加密文件。

这些差异均由独立SQL、官方SDI与真实文件核对。源码依据包括[早期索引字段序列化](https://github.com/mysql/mysql-server/blob/mysql-8.0.12/sql/dd/impl/types/index_element_impl.cc#L120)与[DD列长度赋值](https://github.com/mysql/mysql-server/blob/mysql-8.0.11/sql/dd/dd_table.cc#L538)，实现入口为[metadata.go](../../metadata.go)。

## 两代INSTANT的结构差异

```mermaid
flowchart LR
    SDI[可信SDI定义] --> Old[instant_col及追加列默认字节]
    SDI --> New[physical_pos及列生命周期]
    Old --> Count[0x80字段计数或原始列数]
    New --> Version[0x40逐行版本]
    Count --> Decode[按实际字段集合解码NULL位图和长度数组]
    Version --> Decode
    Decode --> Current[当前列值及缺失字段默认补全]
```

旧式`instant_col`是首次INSTANT之前的存储用户列数，排除VIRTUAL和系统列。多次追加后这个数不会每次加一；各新增列的`default`/`default_null`记录当时的InnoDB默认字节。后续`ALTER ... SET DEFAULT`改变新插入语义，不能改变旧行的补全值。

| 位置 | 长度 | 编码 | 旧式含义 |
|---|---|---|---|
| 记录origin−5 | 1 | 位标志 | 0x80表示存在字段计数；0x20仍为delete-mark |
| origin−6 | 1 | 高位为长度标志 | ≤127字段直接保存计数；高位1表示两个字节 |
| origin−7及origin−6 | 2 | 低字节在前，高字节带0x80 | `((b[origin−6]&0x7f)<<8) | b[origin−7]` |
| 字段计数前 | 按实际可空字段数向左 | NULL位图 | 只包含该行实际存储的列 |
| NULL位图前 | 向左变长 | 长度数组 | 只描述实际存储的变长字段 |

计数包含聚簇字段和系统字段。有显式聚簇键时减去DB_TRX_ID、DB_ROLL_PTR两项得到存储用户列数；隐藏DB_ROW_ID时减去三项。无0x80的旧行使用`instant_col`。范围之外的计数、两个字节却编码≤127、0x80/0x40混用或叶记录MIN_REC明确拒绝。非叶子记录不包含追加非键列，NULL位图使用最初布局；[tree.go](../../tree.go)复用`initialColumns`处理两代INSTANT。

现代8.0.30/45继续使用[第61章](61-instant-row-layout.md)的物理位置、ADD/DROP生命周期和0x40行版本。旧式计数不是DDL次数，不写入`Record.RowVersion`。当前Schema新增`legacy_instant`：`columns`为原存储用户列数，`defaults`按追加顺序保存`InstantDefault`；它与`instant`互斥。自动入口由SDI生成此结构，通常无需手工制作。`DefaultColumns`标明补全来源，合成默认字节计入扫描和回表的行预算。

源码依据为[旧式计数写入](https://github.com/mysql/mysql-server/blob/mysql-8.0.28/storage/innobase/include/rem0rec.ic#L1142)及[原始列数核对](https://github.com/mysql/mysql-server/blob/mysql-8.0.28/storage/innobase/dict/dict0dd.cc#L1693)，实现见[instant.go](../../instant.go)、[record.go](../../record.go)、[schema.go](../../schema.go)。

## 真实字节定位

以下来自`testdata/mysql80-patches/8.0.28`，完整位置、列私有属性与字节见[物理示例](../mysql80-patch-validation/physical-examples.json)。页大小16384，聚簇根为页4；文件绝对偏移等于`4×16384+origin`。所有origin及其负偏移都是页内位置。

`instant_dynamic_mixed`表级`instant_col=2`，当前用户列为id、v、a、b、c、d。id=1的origin=267（文件偏移65803），五字节头为`80 00 30 ff 8e`，origin−6保存`06`：六字段减两系统项，只存id/v/a/b。c由`default=e7958cf09f9880`补出“界😀”，d由`default=8000002a`解出42。id=2的origin=153，头为`00 00 18 00 1a`，没有计数，使用原两列并补全a/b/c/d。id=4的origin=219，计数为`08`，当前六用户列均已存储。

`wide_dynamic_current`原有127用户列，新增一列。新行origin=181（文件偏移65717），origin−7..−6为`82 80`，解为130字段，再减两系统项得到128用户列。不能把这两个字节当成NULL位图，也不能以当前列数解释没有计数的旧行。

## LOB与SDI删除标志

新式LOB页在8.0.11已有实现，本轮不把它的引入归到8.0.13。8.0.11普通LOB插入代码初始化引用/首部版本为1，但部分索引项保持0；8.0.12插入路径显式写入1。当前仅当页零文件头偏移8的四字节大端`FIL_PAGE_SRV_VERSION=80011`时允许索引项0，引用/首部版本仍须非零、当前引用仍不得要求undo。首部和索引项位置沿用[第59章](59-updated-lob-layout.md)。读取器不会从记录本身猜版本，也不全局放开0。

依据为[8.0.11插入路径](https://github.com/mysql/mysql-server/blob/mysql-8.0.11/storage/innobase/lob/lob0impl.cc#L856)、[8.0.12显式初始化](https://github.com/mysql/mysql-server/blob/mysql-8.0.12/storage/innobase/lob/lob0impl.cc#L945)和[文件头版本字段](https://github.com/mysql/mysql-server/blob/mysql-8.0.11/storage/innobase/include/fil0types.h#L53)，实现为[lob.go](../../lob.go)。负例将同一快照的writer标记改为80012并重新封装CRC，零项随即报ErrCorrupt。

8.0.23/28的多次ALTER真实快照还可包含未purge的delete-marked旧SDI。原始`ReadSDI`现在将它们放入`DeletedRecords`，当前`Records`及自动Schema发现只使用未删除对象。删除对象仍验证链/边界、长度、外部载荷和zlib/JSON，并计入总对象数与总字节预算；这不提供undo或历史字典恢复。MIN_REC只允许非叶子记录，叶SDI的INSTANT/行版本标志仍拒绝。

## GIPK与INVISIBLE

8.0.23/28/30实测普通INVISIBLE及不可见STORED列的物化值；未索引VIRTUAL继续只能走显式仅物化入口。8.0.30 GIPK是可见于DD的不可见`my_row_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY`，复用普通八字节用户主键解码。它与InnoDB隐藏六字节`DB_ROW_ID`不同，不将二者混为一个类型。本轮GIPK样本为DYNAMIC；不推定其他组合。

## 验证与范围

每补丁都使用独立官方amd64服务器，在Apple Silicon的Docker中运行；准确运行版本、镜像ID/摘要与官方工具来源单独保存。每表在同一会话持有`FLUSH TABLES ... FOR EXPORT`锁时取得SQL、文件和官方SDI；SQL精确整数/小数、二进制HEX、空间显式轴序和NULL与Go输出逐值对照。测试工具使用Docker/Python不改变产品二进制的运行依赖。重现入口为[生成脚本](../../scripts/generate_mysql80_patch_fixtures.py)、[Go测试](../../mysql80_patch_test.go)与[CLI验证脚本](../mysql80-patch-validation/verify-cli.py)，实际结果集中于[8.0补丁报告](../mysql80-patch-validation/report.json)和[证据说明](../mysql80-patch-validation/README.md)。

新增范围为准确七补丁的16 KiB、非压缩非加密、DYNAMIC/COMPACT单表代表组合：数值、文本/二进制、日期时间、ENUM/SET、JSON、POINT、NULL，普通及隐藏聚簇、多层树、DESC二级/字符前缀、覆盖/回表、LOB插入/小更新/替换/删除、生成列和上述DDL。类型精度与组合以固定SQL样本为准，不将8.0.45完整组合矩阵自动外推。

8.0.29已于2022-04-26发布，之后撤回官方下载。[官方发布说明](https://dev.mysql.com/doc/relnotes/mysql/8.0/en/news-8-0-29.html)说明新增列的InnoDB表数据可能被错误解释，并要求改用8.0.30。当前排除8.0.29，不安排其包体搜索、源码构建或兼容验收；现代行版本仍以已验收8.0.30/45为依据。机制的引入版本与本项目的验收版本分别记录。

分区集合后续扩展与旧版本DDL限制见[第94章](94-partition-version-compatibility.md)。其他中间补丁、跨版本升级文件、函数/多值索引、系统/通用表空间、压缩/加密和redo/undo恢复保持非目标；系统文件迁移知识见[第90章](90-mysql80-system-tablespace.md)。当前范围以[支持矩阵](83-release-support-matrix.md)为准。
