# 61 INSTANT ADD/DROP：逻辑列序与物理行版本

学习目标：在同一页中读出不同DDL时期写入的行，理解新增列为什么可能没有任何本地字节，以及删除列为什么仍影响旧记录的偏移。

前置：[03 记录](03-page-to-record.md)、[04 列值](04-record-to-values.md)、[43 SDI到schema](43-sdi-schema.md)、[57 delete-mark](57-delete-mark-layout.md)。这里的行版本是DDL布局版本，与事务版本、LOB版本均不同。

## 1. “即时”改变了什么

INSTANT ADD/DROP修改数据字典，不要求重写所有既有记录。因此不能把当前 `SHOW CREATE TABLE` 的列序直接套在所有记录上。

当前阶段支持MySQL8.0.45原生的行版本格式（8.0.29起引入的机制），16KiB、DYNAMIC、非压缩非加密表；仍要求稳定快照且目标写事务已结束。不是对所有MySQL8.0版本的兼容承诺。

贯穿实例 `lesson`：

```sql
CREATE TABLE lesson (
  id INT PRIMARY KEY, a VARCHAR(30), b INT NOT NULL, payload LONGTEXT
) ROW_FORMAT=DYNAMIC;
-- 先插入版本0的记录。
ALTER TABLE lesson
  ADD c INT NOT NULL DEFAULT 77 FIRST,
  ADD d VARCHAR(30) NULL DEFAULT NULL AFTER id,
  ADD e VARBINARY(20) NOT NULL DEFAULT X'00FF', ALGORITHM=INSTANT;
-- 插入版本1记录，再改变c的当前SQL默认值。
ALTER TABLE lesson ALTER c SET DEFAULT 99, ALGORITHM=INSTANT;
ALTER TABLE lesson DROP a, DROP payload, DROP c, ALGORITHM=INSTANT;
ALTER TABLE lesson ADD a VARCHAR(30) NULL DEFAULT 'again' FIRST,
  ALGORITHM=INSTANT;
```

这些操作产生布局版本1、2、3。只改SQL默认值没有增加行布局版本；旧行也不会追溯采用99。

## 2. 三套顺序

在 `lesson_readd` 中当前用户列顺序为 `a,id,d,b,e`，但完整物理位置如下：

| physical_pos | 字段 | Added | Dropped | 当前逻辑下标 |
|---:|---|---:|---:|---:|
| 0 | id | 0 | — | 1 |
| 1 | DB_TRX_ID | — | — | 系统字段 |
| 2 | DB_ROLL_PTR | — | — | 系统字段 |
| 3 | 旧a | 0 | 2 | 已删除 |
| 4 | b | 0 | — | 3 |
| 5 | payload | 0 | 2 | 已删除 |
| 6 | c | 1 | 2 | 已删除 |
| 7 | d | 1 | — | 2 |
| 8 | e | 1 | — | 4 |
| 9 | 新a | 3 | — | 0 |

FIRST只是SQL列的位置，新增字段的物理位置追加到末尾。新a不能按名字复用旧a的物理位置。当前SDI索引元素列表也不是所有历史物理字段的完整列表：被DROP的字段需从隐藏列定义中找回。

读取版本v的一行时，字段实际存在的规则是：

```text
Added <= v，并且（未DROP 或 v < Dropped）
```

- 当前还存在但 `Added > v`：该行没有字段字节，使用ADD时默认值。
- 已DROP但当时存在：必须消费其NULL/长度/载荷，使后续字段正确对齐；不返回它的值。
- 当时尚未ADD或已DROP：不占NULL位、不占长度项、不占载荷。

系统字段和完整聚簇键位置固定。本阶段不借INSTANT实现主键变更、前缀聚簇键或生成列。

## 3. SDI中的真实证据

以下摘自交付快照 `lesson_readd.sdi.json.gz`，省略容易变化的table_id：

```text
新a：default=616761696e;physical_pos=9;...;version_added=3;
d：default_null=1;physical_pos=7;...;version_added=1;
e：default=00ff;physical_pos=8;...;version_added=1;
旧c：physical_pos=6;version_added=1;version_dropped=2;
旧a：physical_pos=3;version_dropped=2;
旧payload：physical_pos=5;version_dropped=2;
```

旧a被重命名为 `!hidden!_dropped_v2_p3_a`，其DD Hidden=2，且可能没有table_id。不能因为没有table_id就丢掉它，也不能把任意hidden列都当作DROP列；须有合法version_dropped和完整类型/物理位置。新增后又删除的c没有保留默认字节，这不妨碍当前值读取：c已经不属于当前用户列，我们只需要判断其是否占物理空间。

当前最大布局版本由列的增加/删除版本取最大值，而不是在table私有属性中猜一个字段。源码：`storage/innobase/include/dict0dd.h:477–515`。

默认值为InnoDB字段编码的十六进制，不是SQL显示文本或MySQL客户端行编码。`storage/innobase/dict/dict0dd.cc:83–127`实现hex编解码，2255–2355附近先转换为InnoDB字段字节，再写入default/default_null及physical_pos。

## 4. 一字节行版本放在哪里

COMPACT记录头仍为origin前5字节：

```text
低地址 -> 变长长度（逆序） | NULL位图 | [行版本1字节] | 固定头5字节 | origin:键+系统+字段
```

`REC_INFO_VERSION_FLAG=0x40` 位于固定头第一个字节的高位；设置时，行版本在 `origin-6`。没有该标志的原生旧行按版本0解释，不凭空扣掉一字节。bit0..3仍是目录所有权计数，不能把整个头字节当作标志。

源码：`storage/innobase/rem/rec.h:123–126` 定义标志；`storage/innobase/include/rem0rec.ic:919–939`读写版本字节。`rec.h:1158–1168`按列生存区间区分物理缺失、DROP和默认字段。

### 真实版本0记录

`lesson_add` 的id1仍在页4，Start=120、origin=129、End=173。版本1的DDL没有移动它。元数据开头为：

```text
14 c0 03 | 00 | 00 00 10 00 32 | 80 00 00 01 ...
长度项      NULL   固定头          id=1
```

旧a长3字节；payload是20字节页外引用，长度项占两字节；NULL位图一字节。没有行版本字节。当前列c/d/e由ADD默认值补齐，`DefaultColumns=[0,2,6]`，本地记录依然只有原来的字段。

### 真实版本1记录

同一快照id3在页4，Start=200、origin=209、End=238：

```text
02 02 | 06 | 01 | 40 00 20 ff 9f |
80 00 00 03 | 00 00 00 00 95 e5 | 82 00 00 00 b9 01 10 |
76 31 | 80 00 00 2c | 80 00 00 4d | 00 ff
```

逐步解码：

1. 固定头首字节0x40，行版本为 `b[209-6]=1`。版本字节的文件偏移是 `4×16384+203=65739`。
2. 当前物理可空字段依次是旧a、payload、d。NULL位图0x06说明payload和d为NULL，a非NULL。
3. 非NULL变长字段为a和e，长度均2；长度数组反向存储。
4. 数据依物理序为id、13字节系统字段、a=`v1`、b=44、c=77、e=`00ff`，NULL字段没有载荷。
5. API再按SQL序输出 `c,id,d,a,b,payload,e`，不是改变原始字节的排列。

`lesson_default_changed` 新id4的c编码为 `80000063`（99），但版本0的id1仍从SDI的ADD默认 `8000004d` 得到77。当前SQL默认值不能替代历史默认字节。

### DROP后再ADD

`lesson_drop` 的版本2记录：Start285、origin293，元数据为：

```text
02 | 01 | 02 | 40 00 30 ...
e长2  d=NULL  v2  固定头
```

a/payload/c都已DROP，不再占位。`lesson_readd` 的版本3记录Start316、origin325，开头为 `05 02 01 03 40 ...`：新a长5，e长2，d=NULL，版本3。新a在载荷末尾，内容 `616761696e` 即again，却位于返回Values的第0列。

## 5. 已删除列的LOB为何不读取

版本0行仍携带payload的20字节本地引用，即使当前DDL已无payload。定位后续字段需要知道这个引用占20字节，但不需要读取它指向的历史LOB。

因此当前解析器对DROP字段只消费本地布局，不解码内容、不跟随页外链、不生成External来源。损坏测试把该历史FIRST页类型改坏，当前列读取仍成功。这里的成功不表示被DROP数据健康或可以恢复。

delete-mark行也仍只输出本地摘要；布局版本用于准确计算Start/End，但不补用户默认值或宣称恢复删除行。RowVersion可以从摘要的Raw与Header定位。

## 6. 代码和API

- `Schema.Instant *InstantLayout`：nil为普通布局；Version是当前版本，Fields列出所有用户物理字段，系统字段不重复存储。
- `InstantField.Position`：包含键与系统字段的完整物理位置；Column为当前SQL列下标。DROP字段用Column=-1和DroppedColumn保存类型。
- `Added/Dropped`：0分别表示初始存在/未删除；Default指针保存历史NULL或原始Data，nil与非NULL空字节不同。
- `Record.RowVersion *uint8`：显式版本字节；nil表示无字节。`DefaultColumns`列出补值来源，避免把字典默认值误认为来自本地记录。
- `Values`、TextBytes、CHAR存储文本、ENUM/SET序号与掩码保持现有类型和逻辑下标；来自字典的值由DefaultColumns说明来源。

`metadata.go` 与 `metadataInstant` 建立当前列和历史字段关系；`validateInstant`检查位置、生命周期、完整键/系统布局与默认编码；`decodeInstantRecord`选该版本的物理字段，复用 `decodeStoredRecord` 和 `decodeStoredValue`，最后映射回当前列序。没有新增依赖或第二套标量解码器。

下一章：[62 非叶子布局、重建与验收](62-instant-validation.md)。
