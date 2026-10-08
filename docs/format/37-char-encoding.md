# 37 CHAR：字符数、物理长度与存储空格

本章学习 utf8mb4 CHAR(N) 为什么需要变长长度数组，以及如何同时保留完整物理文本和通常 SQL 显示。前置内容是 [记录列值](04-record-to-values.md)、[变长长度](11-variable-lengths.md) 和 [页外引用](13-lob-reference-and-pages.md)。本章范围为 MySQL 8.0.45、16 KiB、DYNAMIC、新建只插入表、可信 schema；N 为 1..255。

## 两种长度与两次空格处理

CHAR(N) 的 N 是字符上限，utf8mb4 一个字符占 1..4 字节。它在这里不是固定 N 字节字段，也不等同于 VARCHAR：非 NULL 物理文本至少保留 N 字节。

MySQL 8.0.45 的 [row0mysql.cc:495–530](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/row/row0mysql.cc#L495) 在 compact 类布局、DATA_MYSQL、mbminlen=1 且 mbmaxlen>1 的分支中，从末尾逐字节移除 0x20，但只在当前字节数大于 N 时继续。因此裁剪终点可能还有空格，也可能剩下多于 N 字节的完整文本。[ha_innodb.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc) 提供列类型到 InnoDB 类型的映射；SQL 字段补空格逻辑见 [field.cc 的 Field_string::store](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L6129)。

```text
SQL CHAR 字段：按字符宽度处理输入并补空格
        ↓ 写入 InnoDB：只裁剪末尾 0x20，字节数不能小于 N
聚簇记录：长度数组 + N..4N 字节文本，或 20 字节页外引用
        ↓ 本库：完整读取/拼接，验证 UTF-8、字符数和物理长度
CharStorage：保留物理文本
        ↓ 仅移除末尾 U+0020
Values：通常 SQL 显示文本（不启用 PAD_CHAR_TO_FULL_LENGTH）
```

下表来自交付的 char_mixed 行值；空格以 `␠` 展示，记号本身不在文件中。

| 类型及值 | 物理字节 | 字节数 | CharStorage | Values |
|---|---|---:|---|---|
| CHAR(5)，a | `61 20 20 20 20` | 5 | `a␠␠␠␠` | `a` |
| CHAR(1)，界 | `e7 95 8c` | 3 | `界` | `界` |
| CHAR(3)，界 | `e7 95 8c` | 3 | `界` | `界` |
| CHAR(5)，界 | `e7 95 8c 20 20` | 5 | `界␠␠` | `界` |

CHAR(3) 的“界”只占一个字符，却已占三字节，所以物理文件无需额外空格。CHAR(5) 的同一个字需要留下两个空格才能保留五字节。不能据 CharStorage 的字符数直接要求等于 N。

空字符串会存成 N 个空格；SQL NULL 不占字段数据，位图独立标记。空字符串、用户输入的纯空格可能成为相同存储，文件不能恢复最初输入的空格数量。只裁剪 ASCII 空格：NBSP、tab 和 NUL 均保留。

## 真实混合记录逐字节定位

交付 [char_mixed schema](../../testdata/chars/char_mixed.json) 的 CHAR 长度依次为 1、3、5、32、63、64、127、192、255，另有整数主键、VARCHAR 和 TEXT。SQL 声明中的主键在中间，物理记录仍先存主键及事务字段。

id=1 位于页 4，页基址为 `4×16384=65536`；Start=903、origin=924、End=1709。origin 前的真实元信息如下，按内存低地址到高地址列出：

```text
14 c0 04 ff 80 c0 80 7f 40 3f 20 05 03 03 | 00 00 | 00 00 18 03 1f
                 变长长度数组              NULL位图      记录头
```

从靠近位图处向前消费长度，依次得到 CHAR 的 3、3、5、32、63、64、127、192、255 字节，接着 VARCHAR 四字节、TEXT 二十字节 external 引用。声明上限 4N 决定是否允许两字节长度，实际长度决定当前条目宽度：CHAR(63) 最大 252 字节，使用单字节；CHAR(64) 最大 256 字节，本行实际只有 64 字节，仍用单字节。192 与 255 分别以反向读取的 `80 c0`、`80 ff` 表示；external 为 `c0 14`。

| 字段 | 页内范围 [起点,终点) | 长度 | 内容 |
|---|---|---:|---|
| 主键和事务字段 | [924,941) | 17 | INT + DB_TRX_ID + DB_ROLL_PTR |
| c0 CHAR(1) | [941,944) | 3 | 界 |
| c1 CHAR(3) | [944,947) | 3 | 界 |
| c2 CHAR(5) | [947,952) | 5 | 界 + 两空格 |
| c3 CHAR(32) | [952,984) | 32 | 界 + 29 空格 |
| c4 CHAR(63) | [984,1047) | 63 | 界 + 60 空格 |
| c5 CHAR(64) | [1047,1111) | 64 | 界 + 61 空格 |
| c6 CHAR(127) | [1111,1238) | 127 | 界 + 124 空格 |
| c7 CHAR(192) | [1238,1430) | 192 | 界 + 189 空格 |
| c8 CHAR(255) | [1430,1685) | 255 | 界 + 252 空格 |
| varying VARCHAR | [1685,1689) | 4 | `20 61 20 20`，保留所有空格 |
| body TEXT | [1689,1709) | 20 | 引用，恢复后为 60000 字节 |

本地数据长度为 `17+744+4+20=785`。c2 的文件绝对偏移为 `65536+947=66483`，相对记录 origin 的偏移为 23。不要将这三种偏移混用。

## API 与实现顺序

schema 列声明示意：

```json
{"name":"name","type":"CHAR","max_chars":5,"nullable":true}
```

[schema.go](../../schema.go) 验证 N 并返回最大物理长度 4N；[variable.go](../../variable.go) 复用原有反向长度解析。[record.go](../../record.go) 的 variableValue 验证完整文本满足：

- UTF-8 有效，字符数不超过 N。
- 字节数在 N..4N。
- 字节数大于 N 时，最后字节不能是 0x20；否则不符合当前新建表的存储裁剪规则。

[tree.go](../../tree.go) 在页外值恢复完成之后，将原始文本保存到 `Record.CharStorage map[int]string`，再用 `strings.TrimRight(stored, " ")` 生成 Values。map 的键为 SQL/schema 逻辑列下标，不是物理字段顺序。非 NULL 空显示值仍有 map 项，NULL 没有项；没有非 NULL CHAR 的记录 map 为 nil。

该裁剪只用于 CHAR，不影响 VARCHAR/TEXT 的尾空格。输出契约不随 MySQL 会话模式改变；CharStorage 也不是启用 PAD_CHAR_TO_FULL_LENGTH 后的 SQL 输出。类型支持仍不包含 CHAR(0)、其他字符集、字符主键、CHAR BINARY 声明解析及自动 schema 恢复。

下一章沿真实页外 CHAR 路径验证完整值，并给出全部声明长度的复现方法。
