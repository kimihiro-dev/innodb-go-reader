# 15 TEXT/BLOB：类型容量与记录长度

## 学习目标与前置概念

读完本章，你可以解释 TEXT 与 VARCHAR 的长度单位为什么不同，为什么 TINYBLOB 的 128 字节值仍要用两字节长度，以及如何区分 NULL、空值和页外引用。先读第 11 章的逆序长度区、第 13 章的 20 字节引用。

本章实测来自 `testdata/large_lob/all_lob_types.ibd.gz`，解压后 SHA256 为 `81dd0b5049261f174d51f43ea9473baf81795bc2e7e7fd862dc60854db116671`。所有偏移均为十进制页内偏移；文件偏移 = 页号 × 16384 + 页内偏移。

## 类型声明不等于实际值长度

| 文本类型（utf8mb4） | 二进制类型 | SQL 类型容量（字节） |
|---|---|---:|
| TINYTEXT | TINYBLOB | 255 |
| TEXT | BLOB | 65535 |
| MEDIUMTEXT | MEDIUMBLOB | 16777215 |
| LONGTEXT | LONGBLOB | 4294967295 |

TEXT 家族的容量按**字节**计算；255 字节最多容纳 63 个四字节 emoji 再加 3 字节，不能解释成 255 个任意字符。VARCHAR 的 `max_chars` 才是字符数；本项目对 VARCHAR 另行验证 UTF-8 字符数。

八种新类型在 schema 中只写类型及可空属性，例如：

```json
{"name":"body","type":"MEDIUMTEXT","nullable":true}
```

它们不接受 `max_chars`、`max_bytes` 或 `unsigned`。`schema.go` 以 uint64 表达类型容量，避免 LONG 上限在 32 位 int 上溢出。实际值仍受文件结构与实现上限约束：当前 `MaxLOBValueBytes=16777216`，即 16 MiB。LONG 的支持范围限于该大小，超出返回 ErrUnsupported；这不是 MySQL 类型上限。MEDIUM 的 SQL 上限比 16 MiB 少一个字节。

## TINY 类型的特殊长度规则

COMPACT/DYNAMIC 记录可以示意为：

```text
低地址 → [逆序变长长度][NULL 位图][5 字节记录头] | origin → [主键][系统字段][其他列]
```

不能仅凭“声明最大字节数是否超过 255”判断长度编码。MySQL 将 TEXT/BLOB 映射为 DATA_BLOB；`DATA_BIG_COL` 对这种内部类型始终成立。因此 TINYTEXT/TINYBLOB 也采用大列规则：

1. 从靠近 NULL 位图的一侧向低地址读第一字节。
2. 第一字节最高位为 0，实际长度就是该字节（0..127）。
3. 最高位为 1，继续向低地址读第二字节；长度为 `(第一字节 & 0x3f) << 8 | 第二字节`。
4. 第一字节的 0x40 表示 external；当前 DYNAMIC 外部字段本地长度必须为 20，随后解析引用，不能把引用当用户值。
5. SQL NULL 的列没有长度项，也没有值字节；空值有一个长度为零的项。

对比 VARBINARY(255)，其 128 字节值的长度项可以是单字节 `80`；TINYBLOB 则是地址递增排列的 `80 80`。两者不能混用规则。事实依据是官方 [data0type.h 的 DATA_BIG_COL](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/data0type.h#L275) 与 [rem0rec.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/rem/rem0rec.cc)。

## 真实记录逐步解码

夹具包含 id 和八种可空列，八个 NULL 标志恰好占一字节。以下记录都在聚簇页 4：

| id | Start | origin | End | 变长长度区（地址递增） |
|---:|---:|---:|---:|---|
| 0 | 120 | 134 | 151 | `00` 重复 8 次 |
| 2 | 190 | 204 | 1237 | `7f` 重复 8 次 |
| 3 | 1237 | 1259 | 2300 | `80 80` 重复 8 次 |
| 4 | 2300 | 2322 | 4379 | `ff 80` 重复 8 次 |
| 5 | 4379 | 4385 | 4402 | 无长度项，NULL 位图 `ff` |

例如 id=4 的元信息完整字节为：

```text
ff80ff80ff80ff80ff80ff80ff80ff80 00 000030080f
[八个两字节长度项]              [NULL] [记录头]
```

逆向先读 `80` 再读 `ff`，还原长度 255。八列各 255 字节，值区共 `4 + 13 + 8×255 = 2057` 字节，恰好等于 `4379−2322`。4 是 INT 主键，13 是 DB_TRX_ID 和 DB_ROLL_PTR。记录全长再加 16 字节长度区、1 字节位图和 5 字节头，共 2079 字节。

id=5 的元信息为 `ff 0000380023`：八列均为 NULL，origin 后仅剩主键和系统字段的 17 字节。id=0 的八列为空值，位图为零，长度区仍占八字节；两者不能折叠。

id=6 的四个文本值各含 63 个 emoji，共 252 字节，二进制值各为 `ff fe 00 00 20`。其长度区是 `05 fc80` 重复四次。它同时证明文本按字节定位，二进制可包含非法 UTF-8、NUL 和尾部空格。

## 从定位到完整返回值

`variable.go/readVariableLength` 接收类型是否属于 DATA_BLOB；`record.go` 负责逆序读取元数据及定位本地字节；`lob.go` 在存在页外引用时拼接完整字节；`variableValue` 最后统一处理输出。

TEXT 返回 string，完整拼接后验证 UTF-8，不能逐块判定字符合法性，因为字符可以跨页。BLOB 返回独立的 []byte，不解释编码也不裁剪空格。空二进制是非 nil 空切片，SQL NULL 是 nil。标准 JSON 将 []byte 编为 Base64，SQL 预期以 HEX 保存，测试解码两者后按真实字节比较。

下一章解释大值超过首个 LOB 页十个索引项时如何继续读取。当前仍不支持其他字符集、压缩/旧 LOB、历史版本和任意事务可见性；完整当前边界见 [需求](../REQUIREMENTS.md)。
