# 57. delete-mark 不等于空闲记录

<a id="57delete-mark-不等于空闲记录"></a>

本章从同一张表的连续快照学习 UPDATE、DELETE 怎样改变物理记录。前置知识是记录链、页目录、NULL/变长元数据和聚簇键；完整验收与树收缩见[第 58 章](58-change-snapshot-validation.md)。

<a id="1-三种状态分开理解"></a>

## 三种状态分开理解

| 状态 | 是否仍在 infimum→supremum 记录链 | 是否计入 PAGE_N_RECS | 本阶段输出 |
|---|---|---|---|
| 未标记删除的记录 | 是 | 是 | `Result.Records` 的完整用户值 |
| delete-mark 记录 | 是 | 是 | `Result.DeletedRecords` 的本地物理证据 |
| purge 后进入 free 链的残留 | 否 | 否 | 只校验 free 链结构，不作为行返回 |

delete-mark 是记录头的标志，并不是“next 指针已把记录摘掉”。如果在校验前过滤删除记录，会破坏页目录的 owned 计数、堆编号、记录空间统计，也可能漏掉错误键范围。

因此解析顺序是：先按完整物理链还原布局，验证目录、堆、空间和所有键顺序；到结果收集阶段，再分开普通行和删除摘要。跨叶子页顺序也覆盖被标记记录，而不是只比较最终返回的普通行。

<a id="2-0x20-标志位不占新的载荷字节"></a>

## 0x20 标志位不占新的载荷字节

普通 COMPACT 记录头是 origin 前五字节。本阶段只额外允许头首字节中的 `0x20`，不改变后续字段布局：

```text
变长长度 | NULL 位图 | [info/n_owned, heap/status, next] | ← origin
聚簇键 | TRX_ID | ROLL_PTR | 普通用户列
```

首字节低四位仍是 n_owned；`0x20` 与低四位可以同时出现。MIN_REC、INSTANT 和行版本标志不能因为存在 delete-mark 就被一并放行。非叶子和 SDI 记录的标志契约也不因本阶段而放宽。

源码：`storage/innobase/rem/rec.h:121` 定义 `REC_INFO_DELETED_FLAG=0x20`；`storage/innobase/include/rem0rec.ic:353–394` 读取/设置该位。以下引用均相对本地 MySQL 8.0.45 源码根。

<a id="3-真实删除记录的完整位置"></a>

## 真实删除记录的完整位置

`testdata/changes/lesson` 的列为 `id INT PRIMARY KEY, n INT NULL, v VARCHAR(1500) NULL`。`lesson_before` 有 id=1..6，每个 v 为 127 个 a。随后提交一组更新：改变长度、NULL 状态，删除 id=3，将 id=4 改为 40，删除并重插 id=5。

`lesson_marked` 的页号为 4。id=3 的摘要为 `Start=430, Offset=437, End=585`，文件绝对 origin 为 `4×16384+437=65973`。下表区间均为**页内偏移，结束位置不包含**：

| 区间 | 实际字节 | 解释 |
|---|---|---|
| 430..432 | `7f 00` | v 的实际长度 127、NULL 位图全零 |
| 432..437 | `20 00 20 00 9b` | delete-mark；heap number=4；next 相对位移 155 |
| 437..441 | `80 00 00 03` | 主键 INT 3 |
| 441..447 | `00 00 00 00 8f 81` | 原始事务字段 |
| 447..454 | `01 00 00 01 43 02 99` | 原始回滚指针 |
| 454..458 | `80 00 00 03` | 本地残留 n 字节 |
| 458..585 | `61` 重复 127 次 | 本地残留 v 字节 |

`next=437+0x009b=592`，仍然到达下一条记录。origin=592 是旧 id=4，其头为 `20 00 28 00 9b`，同样尚未被摘链。新 id=40 位于 origin=1215；不能认为一次主键更新只是原地修改了四个键字节。

本地残留在这个受控案例中恰好能与变更前 SQL 内容对照，但 API 不把它包装成“恢复出的历史行”：删除时事务字段已经改变，复杂类型可能涉及旧页外版本，完整历史还需要 undo 和可见性规则。

<a id="4-update-不保证-origin-不变"></a>

## UPDATE 不保证 origin 不变

同表各行的位置变化如下：

| 行/变更 | before origin | marked origin | 现象 |
|---|---:|---:|---|
| id=1：v 127→128 字节 | 127 | 1058 | 增长跨过两字节长度边界，记录迁移 |
| id=2：n、v 都改 NULL | 282 | 281 | 变长长度不再存在，origin 可向前移动 |
| id=4→40，v 改为 400 个“长” | 592 | 1215（新键40） | 旧键4仍标记删除，新键另存 |
| id=5：删除后重插同键 | 747 | 747 | 当前记录复用，不能要求每次DELETE留下独立残留 |
| id=6：v 改为空串，n=NULL | 902 | 902 | 空值仍占一个零长度字节，区别于NULL |

id=1 的新 `Start=1050, Offset=1058, End=1207`，而旧位置进入 free/碎片管理。id=2 的新 `Start=275, Offset=281, End=298`，两列 NULL 后仅剩键和事务字段。正确解析应从当前链指针、位图和长度数组计算位置，不能缓存上一快照的行偏移。

<a id="5-purge-才会从在链记录数中移除删除记录"></a>

## purge 才会从在链记录数中移除删除记录

同一根页在四个实际快照中的状态：

| 快照 | 普通行 | delete-mark | PAGE_N_RECS | PAGE_FREE | PAGE_GARBAGE | HeapTop |
|---|---:|---:|---:|---:|---:|---:|
| lesson_before | 6 | 0 | 6 | 0 | 0 | 1050 |
| lesson_marked | 5 | 2 | 7 | 127 | 535 | 2436 |
| lesson_purged | 5 | 0 | 5 | 592 | 845 | 2436 |
| lesson_reused | 7 | 0 | 7 | 437 | 816 | 2721 |

purge 前，id=3 和旧 id=4 各占 155 字节。purge 后垃圾字节增加 `845−535=310=155×2`，活动链记录数从 7 降为 5；旧字节尚在文件中，但不再属于用户记录链。

之后插入 id=4 时复用 origin=592；新记录 `Start=585, End=614`，只用 29 字节，所以垃圾量减少 29，剩余空间仍可表现为碎片。插入 id=3 的新 256 字节文本放在堆尾，origin=2444，不能因键相同就找到旧 origin=437。

源码 `storage/innobase/page/page0cur.cc:2380–2427` 将删除记录从链中摘除；`storage/innobase/include/page0page.ic:890–921` 将其加入 PAGE_FREE、增加 PAGE_GARBAGE 并减少 PAGE_N_RECS。free 指向的是空闲记录 origin，垃圾量还包括碎片；不能只用 free 链长度估算垃圾字节，更不能把全部残留强行解码为历史行。

<a id="6-删除摘要的-api-语义"></a>

## 删除摘要的 API 语义

`Result.DeletedRecords` 的成员为 `DeletedRecord`，包含 PageNumber、Start/Offset/End、Header、完整 Key、可选 RowID、Transaction/RollPointer 和独立 `Raw`。Raw 覆盖本地 `[Start,End)`，包括长度、位图、头和本地载荷；其 origin 相对 Raw 的下标是 `Offset−Start`。

该类型没有 Values。普通非键字段只计算其本地布局，不解释残留标量、不跟随页外引用。完整键仍按类型验证，以便继续检查树顺序和范围。CHAR 键的公开值仍按既有规则去尾空格，二进制 Key 与 Raw 各自拥有独立字节；修改结果不会改变源快照或另一种表示。

`Result.Records` 和示例 auto 的输出只包含未标记行；`Page.Records` 保持物理含义。对于本阶段支持的叶子，整个结果满足：所有叶子的 Page.Records 总和 = len(Records) + len(DeletedRecords)。这条等式不能用于非叶子导航记录。

入口：[record.go](../../record.go) 识别标志、计算布局；[tree.go](../../tree.go) 在完整校验后分流；[examples/changes](../../examples/changes/main.go) 输出普通行数、delete-mark 数、页数及垃圾量。受控快照的获取与事务边界见下一章。
