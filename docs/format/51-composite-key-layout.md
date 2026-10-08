# 51：复合整数主键与记录物理顺序

本章把“一个主键整数”扩展为“按索引定义排序的整数元组”。前置知识是[整数编码](09-integer-values.md)、[非叶子导航记录](07-node-pointers.md)和[SDI 到 schema](43-sdi-schema.md)。树范围与验收见[第 52 章](52-composite-key-validation.md)。

## 1. 主键顺序与列声明顺序是两回事

真实 [composite_lesson](../../testdata/composite/composite_lesson.sql) 的列声明顺序为：

```sql
text VARCHAR(32), b BIGINT UNSIGNED, n INT,
a SMALLINT, c MEDIUMINT, tail VARCHAR(32),
PRIMARY KEY (a,b,c)
```

主键成员都为 NOT NULL，其余列可 NULL。物理记录不能按上面六个用户列的声明顺序直接读取。当前新建 DYNAMIC 表的聚簇叶子记录组织为：

```text
记录头之前：非主键字段的变长长度、NULL 位图、五字节记录头
record origin
  a SMALLINT             2 字节 ┐
  b BIGINT UNSIGNED      8 字节 ├ 主键索引顺序
  c MEDIUMINT            3 字节 ┘
  DB_TRX_ID              6 字节
  DB_ROLL_PTR            7 字节
  text                  可变   ┐
  n                     4或0   ├ 剩余用户列的声明顺序
  tail                  可变   ┘
```

已经出现在键中的列不再重复存储。主键都非 NULL 且为定长整数，不占 NULL 位，也不消费变长长度，因此剩余可空列和变长列仍可按它们在原声明中的相对顺序读取。

本 API 的 Values 继续按 SQL 声明顺序返回：`[text,b,n,a,c,tail]`。解码时必须把每个键成员放回它对应的逻辑位置；物理顺序与输出顺序不能共用同一个简单递增下标。

## 2. 从真实字节还原第一行

该夹具按主键排序的第一行是：

```text
["before", 0, null, -32768, -8388608, "after"]
主键 = (-32768,0,-8388608)
```

它在页 4，Start=210、origin=218、End=255。origin 的文件绝对偏移为 `4×16384+218=65754`。字段偏移如下；括号采用左闭右开：

| 字段 | 页内区间 | 字节数 | 实际字节/解码 |
|---|---|---:|---|
| a | [218,220) | 2 | `00 00` → SMALLINT 最小值 -32768 |
| b | [220,228) | 8 | 八个00 → UNSIGNED 0 |
| c | [228,231) | 3 | 三个00 → MEDIUMINT 最小值 -8388608 |
| DB_TRX_ID | [231,237) | 6 | `00 00 00 00 71 8a` |
| DB_ROLL_PTR | [237,244) | 7 | `81 00 00 00 f9 01 40` |
| text | [244,250) | 6 | `62 65 66 6f 72 65` → before |
| n | 无载荷 | 0 | NULL |
| tail | [250,255) | 5 | `61 66 74 65 72` → after |

这里整个键宽为 `2+8+3=13`。系统字段从 `origin+13` 开始，而不是原 INT 单列样本的 `origin+4`。系统字段本身没有变宽。

记录 origin 前八字节为：

```text
05 06 | 02 | 00 00 20 00 e1
长度    NULL   五字节头
```

从 NULL 图向左逆序读取长度：text=6、tail=5。可空列顺序为 text、n、tail，NULL 图 `02` 表示仅 n 为 NULL。

同一主键前缀的下一行位于页内 origin=443，键为 `(-32768,0,0)`。前十字节仍全零，c 的三字节变为 `80 00 00`，符号位翻转后还原整数零。此处明确展示：共享前缀并不表示重复主键，必须继续读第三个成员。

## 3. 非叶子记录使用完整键元组

非叶子不是用户行。它存储：

```text
保留 NULL 位图 | 五字节头 | a | b | c | child_page_no
                                      ↑ 大端4字节
```

其中没有 DB_TRX_ID、DB_ROLL_PTR 或普通用户字段。NULL 位图仍按当前索引的可空字段数量预留；本阶段所有主键非 NULL，所以保留位必须为零。

真实 `composite_tree` 主键为 `(a SMALLINT,b SMALLINT,c INT)`，键宽8字节。根页4的一条有限导航记录位于 origin=270，Start=264、End=282，原始字节为：

```text
00 | 00 00 51 ff b8 | 80 00 | 80 04 | 80 00 00 04 | 00 00 00 0d
NULL   记录头          a=0     b=4       c=4            child=13
```

子页号从 `origin+8` 开始，导航数据长 `8+4=12`。如果仍按单列键宽读取，可能把 b/c 的值误当子页号；CRC 校验无法替代这种布局校验。

本阶段 `NodePointer.Key` 为 `[]any{int16(0),int16(4),int32(4)}`，类型保持各成员的实际 Go 整数宽度。MIN_REC 的特别含义在下一章解释。

## 4. API：兼容旧单列，显式给出元组

手工 schema 新写法：

```json
{
  "primary_keys": ["a","b","c"],
  "columns": [
    {"name":"text","type":"VARCHAR","max_chars":32,"nullable":true},
    {"name":"b","type":"BIGINT","unsigned":true},
    {"name":"n","type":"INT","nullable":true},
    {"name":"a","type":"SMALLINT"},
    {"name":"c","type":"MEDIUMINT"},
    {"name":"tail","type":"VARCHAR","max_chars":32,"nullable":true}
  ],
  "root_page": 4, "space_id": 233, "index_id": 444
}
```

这些 space/index/root 数字来自本轮夹具，不是通用常量。完整文件见 [composite_lesson.json](../../testdata/composite/composite_lesson.json)。

- `PrimaryKey string` / `primary_key` 保留单列历史接口。
- `PrimaryKeys []string` / `primary_keys` 按主键索引顺序列出1..16个成员；也允许单元素。
- 两种非空声明互斥，至少提供一种；缺失、重复、未知列名、可空或非整数成员明确拒绝。
- 单列 NodePointer.Key 仍为原整数标量；多列为有序 `[]any`。
- 主键数组不能按列名排序，也不能按 Columns 下标重新排序。

自动入口按 SDI 索引元素顺序提取所有非 hidden 主键元素，核对每列整数宽度、升序和非 NULL。再核对物理元素顺序：全部键、两个系统字段、所有非键用户列；普通字段仍需符合已有类型矩阵。单列自动 schema 仍输出旧 PrimaryKey，多列才输出 PrimaryKeys。

## 5. 实现与源码依据

[ schema.go ](../../schema.go) 验证并返回有序主键列下标；[record.go](../../record.go) 依序读取所有键、还原到逻辑列位置后跳过这些列；[tree.go](../../tree.go) 读取完整非叶子键与子页；[metadata.go](../../metadata.go) 构建并核对 SDI 布局。

官方源码均以用户本地 MySQL 8.0.45 为基线：

- `storage/innobase/dict/dict0dict.cc:3000–3110`：聚簇索引键之后加入事务/回滚字段，随后处理其余列。
- 同文件 `:3683–3707`：导航元组拷贝完整唯一前缀，再附加4字节子页号；比较字段数不含子页号。
- `sql/sql_const.h:46`：MAX_REF_PARTS 为16。

本阶段不改变整数的符号翻转与大端编码。它改变的是字段排列、键宽度和比较单位。字符串、DESC、前缀键和隐藏聚簇键仍由后续阶段处理。
