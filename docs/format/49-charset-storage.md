# 49. 字符集、原始字节与 CHAR 布局

<a id="49字符集原始字节与-char-布局"></a>

本章解决两个问题：同一字节怎样变成正确的文字，以及字符集为什么会改变 CHAR 的物理布局。前置知识是[变长字段](11-variable-lengths.md)、[CHAR 空格](37-char-encoding.md)和[SDI 到 schema](43-sdi-schema.md)。[第 50 章](50-charset-validation.md)给出零宽字段、LOB 和验证方法。

<a id="1-字节编码排序规则分别负责什么"></a>

## 字节、编码、排序规则分别负责什么

```text
记录头 / NULL 位 / 固定宽度或变长长度
                  ↓
       完整字段字节（可能来自 LOB）
                  ↓
       Column.Charset 决定如何解码
         ├─ TextBytes：保留原始字节
         └─ UTF-8 Go string
              ├─ CHAR：CharStorage 保留实际文本与残留空格
              │         Values 去尾 U+0020
              └─ VARCHAR/TEXT：Values 保留全部文本
```

字符集规定编码，如字节 `80` 在 MySQL latin1 中表示欧元符号。排序规则规定比较权重；`latin1_swedish_ci` 和 `latin1_bin` 用相同字符编码，但比较行为不同。解析器目前只还原值，不实现字符主键或排序规则比较。

Go string 是字节序列，语言本身不保证里面是 UTF-8；本 API 明确返回有效 UTF-8 文本。不能直接 `string(raw)` 处理 latin1，否则 `0x80` 会成为非法 UTF-8，而 JSON 编码还可能把它替换掉。也不能以解码后 Go 字符串的字节长度推断文件字段长度。

本阶段支持范围：

| Charset | 单字符最大字节数 | 解码检查 |
|---|---:|---|
| 省略或 utf8mb4 | 4 | 严格 UTF-8，包括补充平面字符 |
| utf8mb3；别名 utf8 | 3 | 严格 UTF-8，但拒绝四字节字符 |
| ascii | 1 | 字节只能在 00..7f |
| latin1 | 1 | MySQL CP1252 兼容映射，所有 256 字节有对应字符 |

这里的 Charset 是 schema 中的小写名称。SQL 的 `utf8` 别名在 8.0.45 SDI 中表现为 utf8mb3 对应的 collation；手工 schema 的 `charset:"utf8"` 也按 utf8mb3 解释。不要把这个别名理解为支持四字节 emoji。

<a id="2-mysql-latin1-不等于-iso-8859-1"></a>

## MySQL latin1 不等于 ISO-8859-1

官方 `strings/ctype-latin1.cc:116–166` 明确使用 CP1252，且将 CP1252 的五个未定义位置映射为同码点控制字符。

| 原始字节 | 返回码点 | UTF-8 字节 |
|---|---|---|
| 80 | U+20AC（€） | e2 82 ac |
| 81 | U+0081 | c2 81 |
| 8d、8f、90、9d | U+008D、U+008F、U+0090、U+009D | 相应两字节 UTF-8 |
| 9f | U+0178（Ÿ） | c5 b8 |
| ff | U+00FF（ÿ） | c3 bf |

除 80..9f 的 32 项映射外，原字节值就是对应 Unicode 码点。这个小表配合标准库即可完成解码，无需引入通用转码库。ASCII 高位字节和 utf8mb3 四字节序列则必须报损坏，不能借用 latin1 的宽松映射。

真实 [bytes_latin1.ibd.gz](../../testdata/charset/bytes_latin1.ibd.gz) 含全部 256 个字节，每行同时写入 CHAR(1)、VARCHAR(1)、TINYTEXT。id=129 对应 80：页 4、origin=5505，三个字段依次位于页内 5522、5523、5524，都是 `80`。三个 Values 都是 `€`；三个 TextBytes 切片都只有一个字节 `80`。

id=130 的 origin=5547，字段起点=5564，三个 `81` 解码成 U+0081，不变成问号，也不丢弃。SQL 独立输出经 `CONVERT(... USING utf8mb4)` 验证，不依赖 Go 解码器生成预期。

<a id="3-单字节-charn-是定长字段"></a>

## 单字节 CHAR(N) 是定长字段

以 [charset_latin1_bin](../../testdata/charset/charset_latin1_bin.sql) 的 id=6 为例。字段依次是：

```text
id, c CHAR(5), v VARCHAR(256), t TEXT,
cz CHAR(0), bz BINARY(0), vz VARCHAR(0), vbz VARBINARY(0),
e ENUM(...), s SET(...), note VARCHAR(32)
```

c/v/t 输入一个欧元符号，e/s 均存数值 3。页 4 origin=386，记录 End=425。主键、事务 ID、回滚指针共 17 字节后，c 从页内 403 开始，文件偏移 `4×16384+403=65939`。

```text
页内 403..407：80 20 20 20 20   CHAR(5)，固定五字节
页内 408：    80               VARCHAR，一个字节
页内 409：    80               TEXT，一个字节
cz/bz/vz/vbz：零字节载荷
页内 410：    03               ENUM 序号
页内 411：    03               SET 位掩码
页内 412..424：after-charset
```

c 不消费变长长度。记录 origin 之前实际有 14 字节：

```text
0d 00 00 00 00 01 01 | 00 00 | 00 00 38 fe ee
变长长度（正向展示）    NULL图    五字节记录头
```

从靠近 NULL 图的一侧向左读：v=1、t=1、cz=0、bz=0、vz=0、vbz=0、note=13。CHAR(5)、ENUM 和 SET 不在这个数组中。

对于 c：

```go
record.Values[1]      // "€"
record.CharStorage[1] // "€    "，有效 UTF-8 字符串
record.TextBytes[1]   // []byte{0x80,0x20,0x20,0x20,0x20}
```

此时 CharStorage 的 Go 字节长度是 7，原始 TextBytes 长度是 5。只有后者能直接对应物理偏移。

<a id="4-utf-8-charn-仍使用变长元数据"></a>

## UTF-8 CHAR(N) 仍使用变长元数据

单字节字符集的 `mbminlen=mbmaxlen=1`。UTF-8 每字符长度不固定，因此在当前 COMPACT/DYNAMIC 记录中，CHAR 参与变长长度数组。

SQL 层先把 CHAR(N) 补到 N 个字符。InnoDB 对这些变宽编码从尾部去掉空格，但不会把字节长度压到 N 以下。本阶段只支持最小字符宽度为 1 的四种编码，不把这个规则推广到 UCS2 等其他字符集。

真实 `charset_utf8mb3_general_ci` id=6，页 4 origin=391，c 从页内 408 开始：

```text
e4 b8 ad 20 20   实际存储“中  ”，五字节
```

SQL CHAR(5) 完整补齐显示是“中    ”（七字节）；物理存储已去掉两个空格。其元数据为：

```text
0d 00 00 00 00 03 03 05 | 00 00 | 00 00 38 fe e9
```

相比单字节例子，多出 c 的长度 `05`。c/v/t 共占 5+3+3 字节，不能按字符数 1+1+1 推进记录位置。

真实 `charset_utf8mb4_bin` 对应 id=6 的 c 是：

```text
f0 9f 98 80 20   “😀 ”，仍五字节
```

这两例的物理字节数相同，Unicode 字符数不同。Values 分别返回“中”和“😀”；CharStorage 保留两个或一个残留空格；TextBytes 保存上面完整字节。

结构检查如下：

- 单字节 CHAR(N>0)：读取固定 N 字节，解码后恰好 N 字符。
- UTF-8 CHAR(N>0)：长度在 N..N×最大字符宽度之间，字符数不超过 N。
- 若物理字节长度大于 N，末尾不应继续存在 20，否则没有遵循当前去空格格式。
- CHAR 的 Values 只裁剪 U+0020，不裁剪制表符、换行或其他 Unicode 空白。

<a id="5-最大字节数决定长度元数据"></a>

## 最大字节数决定长度元数据

VARCHAR(N) 中 N 是字符数，不是字节数。其物理最大字节数为 `N×charsetWidth`：

| 定义 | 最大字节数 | 非空值长度元数据 |
|---|---:|---|
| VARCHAR(255) ascii | 255 | 始终一字节，包括 ff=255 |
| VARCHAR(256) ascii | 256 | 长度 ≥128 时两字节 |
| VARCHAR(85) utf8mb3 | 255 | 始终一字节 |
| VARCHAR(86) utf8mb3 | 258 | 长度 ≥128 时两字节 |
| VARCHAR(63) utf8mb4 | 252 | 始终一字节 |
| VARCHAR(64) utf8mb4 | 256 | 长度 ≥128 时两字节 |

TINYTEXT 虽然最大也是 255 字节，仍沿用 DATA_BLOB 的大列长度规则，不能照搬 VARCHAR(255)。TEXT 家族容量按字节计算，字符集只负责解码，不把 TEXT 的容量乘以字符宽度。

当前声明限制为 CHAR 0..255 字符、VARCHAR 0..floor(65535/字符宽度) 字符；BINARY 0..255 字节、VARBINARY 0..65535 字节。声明上限不保证所有列组合满足 MySQL 行大小限制；页外值仍有本项目 16 MiB 单值预算。

<a id="6-sdi-与字典列"></a>

## SDI 与字典列

自动入口只映射实测排序规则：

| collation ID | 名称 | 输出 Charset |
|---:|---|---|
| 255、45、46 | utf8mb4_0900_ai_ci、utf8mb4_general_ci、utf8mb4_bin | 省略，沿用默认 utf8mb4 |
| 33、83 | utf8mb3_general_ci、utf8mb3_bin | utf8mb3 |
| 11、65 | ascii_general_ci、ascii_bin | ascii |
| 8、47 | latin1_swedish_ci、latin1_bin | latin1 |

原始 collation ID 仍在元数据报告中。schema 中省略默认值是为保持历史夹具兼容，不代表丢失了报告里的排序规则。

DD `char_length` 为最大**字节数**。CHAR/VARCHAR 映射必须验证整除字符最大宽度，再得出 MaxChars；例如 utf8mb3 CHAR(5) 的 char_length=15，而 latin1 CHAR(5)=5。未知 ID 返回 Issues，不凭排序规则名称猜测编码。

ENUM/SET 的记录仍是序号/掩码；新增的是 SDI 字典按对应字符集解码，不能默认 Base64 解码后的字节一定是 UTF-8。Values 的标签、EnumIndexes 和 SetMasks 保持原接口，TextBytes 不保存字典列的伪文本载荷。手工 schema 的字典标签使用 UTF-8 字符串，并检查是否可用指定字符集表示。

入口见 [charset.go](../../charset.go)、[schema.go](../../schema.go)、[metadata.go](../../metadata.go)、[record.go](../../record.go)、[tree.go](../../tree.go)。官方源码依据：`storage/innobase/include/data0type.ic:435–473` 固定/变宽类型判断；`storage/innobase/row/row0mysql.cc:495–530` 多字节 CHAR 去空格；`storage/innobase/rem/rem0rec.cc:883–934` fixed_len 与长度数组；`sql/dd/dd_table.cc:723–735` 字典元素保存。Java 图谱用于定位 CharsetMapping/CollationMapping，格式结论以本地官方 8.0.45 源码和真实字节为准。
