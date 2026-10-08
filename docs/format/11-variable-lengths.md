# 11. 页内变长字段：从长度区找到文本与二进制

本章接续第 04、09 章。目标是读懂同一条记录里的一字节与两字节长度，理解字符数和字节数的区别，并完整恢复页内 VARCHAR 与 VARBINARY。主键仍是整数，不涉及字符串索引的排序规则。

<a id="1-扩展后的支持范围"></a>

## 扩展后的支持范围

| 类型 | 显式 schema 属性 | 声明范围 | Go 值 |
|---|---|---|---|
| utf8mb4 VARCHAR | max_chars | 1..16383 个字符 | string |
| VARBINARY | max_bytes | 1..65535 字节 | 独立 []byte |
| 五种整数及 UNSIGNED | 见第 09 章 | 不变 | 对应 Go 整数 |

VARCHAR 的 max_bytes 必须零；VARBINARY 的 max_chars 必须零；两种变长类型都不接受 unsigned。整数的这两个长度属性都必须零。旧 schema 省略 max_bytes 时值为零，继续兼容。

示例列元数据：

```json
[
  {"name":"text","type":"VARCHAR","nullable":true,"max_chars":1024},
  {"name":"payload","type":"VARBINARY","nullable":true,"max_bytes":2048}
]
```

声明范围只是解析器的元数据契约，不表示每个组合都能在 MySQL 建表，也不保证每个值都保存在页内。实际建表还受行总长等限制；实际存储受记录大小与行格式影响。这里仍限定 MySQL 8.0.45、16 KiB、DYNAMIC、可信显式 schema、只插入的新表快照。

VARCHAR 的声明长度是字符数，VARBINARY 是字节数。SQL 层语义可参考 [VARCHAR 手册](https://dev.mysql.com/doc/refman/8.0/en/char.html)和 [VARBINARY 手册](https://dev.mysql.com/doc/refman/8.0/en/binary-varbinary.html)。下面的 COMPACT 记录长度位编码则以 MySQL 源码为准，不能直接照搬 SQL 层“长度前缀”的笼统说明。

<a id="2-判断长度字节数需要两种长度"></a>

## 判断长度字节数需要两种长度

定义：

- M：列声明允许的最大**字节数**。本项目 VARCHAR 为 `max_chars×4`，VARBINARY 为 max_bytes。
- L：当前这个非 NULL 字段实际占用的数据字节数。

MySQL mysql-8.0.45 [rem0rec.cc:891–933](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/rem/rem0rec.cc#L891) 给出了写入规则：

| 条件 | 长度区字节数 | 解读 |
|---|---:|---|
| 字段为 NULL | 0 | 仅看 NULL 位图，不消耗长度字节 |
| M ≤ 255 | 1 | 整个字节是 L，最高位也属于长度 |
| M > 255 且 L < 128，页内 | 1 | 字节是 L |
| M > 255 且 L ≥ 128，页内 | 2 | 第一读到的字节带 0x80 标志，其余位与下一字节合成 L |
| 外部存储 | 2 | 同时设置 0x80 和 0x40；本阶段明确拒绝 |

这解释了两个容易混淆的例子：

- utf8mb4 VARCHAR(63) 的 M=252，存 63 个 emoji 时 L=252，长度字节 fc。此处 fc 的高位**不是**页外标志。
- utf8mb4 VARCHAR(64) 的 M=256，若当前值 L=128，则需要两字节长度。不是等到实际值超过 255 字节才改用两字节。

实际页内两字节编码的合并公式为：

```text
first = 从靠近 NULL 位图的一端读取的字节
second = 再向低地址读取的字节
external = (first & 0x40) != 0
L = ((first & 0x3f) << 8) | second
```

只有 M>255 且 first 的 0x80 位已设置时，才应用这段规则。读取第二字节前必须验证边界；页外标志不能被当作长度高位。

<a id="3-长度区逆序排列"></a>

## 长度区逆序排列

物理数据按主键、系统字段、其余列顺序向高地址存放；长度元数据从靠近 NULL 位图的位置向低地址读取。因此有两重“逆序”：多个变长字段的长度反向排列；双字节长度的高位字节位于较高地址，先被读取。

```text
低地址                                                    高地址
[最后列长度] ... [第二列长度][第一列长度][NULL 位图][记录头]
                                                            origin→[主键][系统字段][各列数据]
```

对 M>255 的列，以下是按地址递增排列的示意编码：

| L | 文件长度字节 | 实际读取次序 |
|---:|---|---|
| 0 | 00 | 00 |
| 127 | 7f | 7f |
| 128 | 80 80 | 80 → 80 |
| 255 | ff 80 | 80 → ff |
| 256 | 00 81 | 81 → 00 |
| 1024 | 00 84 | 84 → 00 |

这不是普通的“从低地址开始读一个大端 uint16”。实现入口 `variable.go/readVariableLength` 按游标逆向消费元数据，并返回下一读取位置。

<a id="4-真实记录同一行同时出现一字节与两字节长度"></a>

## 真实记录：同一行同时出现一字节与两字节长度

交付样本 `testdata/variable/length_rows.ibd.gz`，解压后使用原始页偏移。SQL 声明顺序为：

```text
short_text VARCHAR(63), long_text VARCHAR(1024), id INT PRIMARY KEY,
b255 VARBINARY(255), b256 VARBINARY(256), payload VARBINARY(2048)
```

id=5 的行位于页 4，Start=3570、origin=3584、End=4876。文件 origin 偏移为 `4×16384+3584=69120`。

Start 到 origin 的真实字节：

```text
00 81 | 00 81 | ff | 00 81 | fc | 00 | 00 00 38 05 1a
payload  b256  b255 long_text short  NULL      记录头
```

长度读取过程：

| 页内偏移（按读取顺序） | 字节 | 解释 |
|---|---|---|
| 3577 | fc | short_text 的 M=252，因此 L=252 |
| 3576 → 3575 | 81 → 00 | long_text 的 M=4096，因此 L=256 |
| 3574 | ff | b255 的 M=255，因此 L=255 |
| 3573 → 3572 | 81 → 00 | b256 的 M=256，因此 L=256 |
| 3571 → 3570 | 81 → 00 | payload 的 M=2048，因此 L=256 |

NULL 位图位于 3578，值为 00，五个可空列都非 NULL。记录头位于 3579..3583，主键虽在声明中间，物理上仍从 origin 开始。

数据定位表：

| 页内起点 | 长度 | 字段与真实数据 |
|---:|---:|---|
| 3584 | 4 | 主键 `80 00 00 05` → 5 |
| 3588 | 6 | DB_TRX_ID `00 00 00 00 65 dd` |
| 3594 | 7 | DB_ROLL_PTR `81 00 00 00 f4 01 51` |
| 3601 | 252 | short_text：`f0 9f 98 80` 重复 63 次 |
| 3853 | 256 | long_text：`61` 重复 256 次 |
| 4109 | 255 | b255：从 00 到 fe，每个字节一次 |
| 4364 | 256 | b256：从 00 到 ff，每个字节一次 |
| 4620 | 256 | payload：从 00 到 ff，每个字节一次 |

终点为 `3584+4+13+252+256+255+256+256=4876`。字段后的下一条记录不一定按物理地址接续，仍按记录链读取。

<a id="5-null空串与跨字节-null-位图"></a>

## NULL、空串与跨字节 NULL 位图

同表 id=8 的 Start=9112、origin=9118，前置字节是：

```text
1f | 00 00 50 00 1c
位图      记录头
```

五个可空字段都为 NULL，因此没有长度字节，也没有字段数据。主键与系统字段占 17 字节，End=9135。

id=0 则所有变长字段都非 NULL：short_text 有 252 字节，其他四列为空，各自仍占用一个 00 长度字节。这两种情况不能互换。

`bitmap_rows` 有十个可空变长列 v0..v9，整数主键声明在最后。id=0、origin=139，NULL 位图在页内 132..133，真实字节 `02 49`；从靠近头部的 49 开始读位，再读 02，表示 v0、v3、v6、v9 为 NULL。它们不消耗长度字节。长度区按低地址到高地址为：

```text
00 81 | 87 80 | 85 80 | 00 81 | 00 81 | 81 80
 v8      v7      v5      v4      v2      v1
```

反向读得 129、256、256、133、135、256 字节。`record.go` 将位图索引与长度游标分开推进，跳过 NULL 列后仍能对齐后续字段。

<a id="6-字符数二进制与返回值"></a>

## 字符数、二进制与返回值

id=9 的 long_text 是 `中文😀`、一个 NUL 和两个空格，共 6 个字符、13 字节。VARCHAR 通过 UTF-8 校验后按字符数检查 max_chars，保留尾部空格与 NUL；不能用 strlen 风格逻辑遇到零字节就停止。

同一行 payload 为 `ff fe 00 00 20`，它不是有效 UTF-8，但合法地属于 VARBINARY。解析器返回独立的 []byte，不对它做文本解码，也不丢失尾部零或空格。

| SQL 值 | Go 值 | 标准 JSON |
|---|---|---|
| NULL | nil | null |
| 空 VARBINARY | 非 nil 的空 []byte | "" |
| 非空 VARBINARY | []byte | Base64 字符串 |
| VARCHAR | string | JSON 文本字符串 |

二进制切片拥有独立内存，直接记录解码也不会把原页缓冲区作为可变视图返回。消费者可用标准 Base64 解码恢复字节；不要把 Base64 当作数据库里存放的文本。整数 JSON 的精度要求继续见第 09 章。

<a id="7-边界与下一章"></a>

## 边界与下一章

`readVariableLength` 拒绝缺失字节、非规范的过短双字节编码和超过声明的长度；`decodeRecord` 继续验证堆区边界、UTF-8 及字符数。声明了较大的最大值也可以存很短的页内数据；发现 external 标志才明确返回 ErrUnsupported。

下一章解释真实页外拒绝样本、变长记录造成的碎片，以及完整复现。未实现页外 LOB、TEXT/BLOB 类型、其他字符集、字符主键或元数据自动发现。
