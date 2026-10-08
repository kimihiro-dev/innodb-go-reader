# 31. BINARY：定长二进制、补零与记录定位

本章目标：从真实记录恢复 BINARY 的全部字节，区分固定宽度、变长字段、空输入和 NULL。前置知识见 [记录到列值](04-record-to-values.md)、[变长字段](11-variable-lengths.md)；验证与复现见 [下一章](32-binary-validation.md)。

## 同为二进制，布局不同

BINARY(M) 的 M 是字节数。本阶段支持 1..255，schema 使用 `max_bytes` 指定；在 BINARY 上它表示精确固定宽度，在 VARBINARY 上才表示允许的最大宽度。BINARY 不使用字符集解码，不等于 SQL 的 `CHAR ... BINARY`。

```json
{"name":"token","type":"BINARY","max_bytes":3,"nullable":true,"max_chars":0}
```

| 特征 | BINARY(3) | VARBINARY(3) |
|---|---|---|
| 非 NULL 数据字节数 | 恒为 3 | 0..3 |
| 变长长度数组项 | 无 | 有 |
| 插入 X'61' 后 | `61 00 00` | `61` |
| 插入 X'' 后 | `00 00 00` | 零字节 |
| SQL NULL | 位图标记，不占值字节 | 位图标记，不占值字节 |
| Go 返回 | 独立 []byte 或 nil | 独立 []byte 或 nil |

MySQL 将不足声明长度的 BINARY 值在右侧补 `00`，读取时保留这些字节。解析器还原存储后的值，不裁剪尾零，也不尝试猜测插入前的长度。例如 `X'61'` 和 `X'6100'` 存进 BINARY(3) 后都为 `61 00 00`，文件无法告诉我们哪一个是原输入。以上行为与 [MySQL 8.0 官方手册](https://dev.mysql.com/doc/refman/8.0/en/binary-varbinary.html) 一致。

尾部空格 `20` 也是普通字节，不能像某些字符列那样去空格。`ff`、无效 UTF-8、内嵌零都合法。返回 []byte 使用标准 JSON Base64，例如 `61 00 00` 输出 `"YQAA"`；NULL 输出 `null`。BINARY(1..255) 的非 NULL 输出不会是零长度切片。

## 字段不携带长度，也不定义字节序

```text
非 NULL BINARY(M)
字段起点 ├──────── 精确 M 字节，原样复制 ────────┤ 下一列

NULL BINARY(M)
位图置位 → 本字段不占值区 → 直接读取下一列
```

与 BIT 或整数不同，BINARY 没有整体数值解释，因此不应该对它做大端、小端或符号位转换。如果应用把某些字节解释为整数，那属于应用协议。InnoDB 读取只保留顺序。

元信息读取阶段必须跳过 BINARY。现有 `variableMaxBytes()` 对它返回 0，表示不参与变长长度数组；之后 `decodeRecord` 用 `MaxBytes` 决定固定读取量。若为了复用 VARBINARY 而让它参与长度数组，会把 NULL 位图之前的其他字段长度甚至记录头当成自己的长度，后面的全部列都可能错位。

## 从交付文件逐字节恢复

样本：[binary_mixed schema](../../testdata/fixed_binary/binary_mixed.json)、[物理文件](../../testdata/fixed_binary/binary_mixed.ibd.gz)、[文件哈希](../../testdata/fixed_binary/manifest.json)。其中 id 在逻辑列中间，物理聚簇记录仍先存主键和两个系统字段。

id=1 位于页 4，Start=640，origin=651，End=1183。页基址是 `4×16384=65536`，字段文件绝对偏移需在下表的页内偏移上加 65536。

| 页内区间 | 相对 origin | 长度 | 内容 |
|---|---:|---:|---|
| [651,655) | 0 | 4 | INT id |
| [655,661) | 4 | 6 | DB_TRX_ID |
| [661,668) | 10 | 7 | DB_ROLL_PTR |
| [668,669) | 17 | 1 | b0 BINARY(1)：`00` |
| [669,671) | 18 | 2 | b1 BINARY(2)：`00 ff` |
| [671,674) | 20 | 3 | b2 BINARY(3)：`00 ff 00` |
| [674,681) | 23 | 7 | b3：`00 ff` 后五个零 |
| [681,689) | 30 | 8 | b4：`00 ff` 后六个零 |
| [689,705) | 38 | 16 | b5：`00 ff` 后十四个零 |
| [705,768) | 54 | 63 | b6：`00 ff` 后六十一个零 |
| [768,896) | 117 | 128 | b7：`00 ff` 后一百二十六个零 |
| [896,1151) | 245 | 255 | b8：`00 ff` 后二百五十三个零 |
| [1151,1153) | 500 | 2 | varying VARBINARY：`00 ff` |
| [1153,1163) | 502 | 10 | note：UTF-8 “定长😀” |
| [1163,1183) | 512 | 20 | body：外部引用 |

b2 的文件偏移是 `65536+671=66207`。按已验证 schema 取三个字节得到 `00 ff 00`，复制为独立切片；不能在 `00` 处结束，也不能删掉最后一个零。然后下一列从页内 674 开始，继续同样的固定长度推进。

九个 BINARY 总长 `1+2+3+7+8+16+63+128+255=483`。整条记录数据区 `17+483+2+10+20=532`，与 `1183−651` 一致。body 的 20 字节引用随后由已有 LOB 逻辑还原成 60000 字节 TEXT，BINARY 自身仍在页内。

id=0 的 b2 在页内 [150,153)，为 `61 00 00`；这是短值 X'61' 的补零样本。对应 SQL HEX 为 `610000`，不是 `61`。这些字节和偏移来自交付文件，重建后的事务元信息、space/index ID 及哈希可以不同。

## 源码和实现入口

核对 MySQL 官方 mysql-8.0.45：

- [field.cc:6129–6150](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L6129)：`Field_string::store` 复制输入后通过字符集的 pad_char 填充剩余空间；binary 的填充值为零。源码注释中的“spaces”不能替代实际 pad_char 语义。
- [ha_innodb.cc:7957–7962](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc#L7957)：二进制 MYSQL_TYPE_STRING 映射为 DATA_FIXBINARY。

Go 仅扩展 [schema.go](../../schema.go) 的类型、长度验证和二进制识别，以及 [record.go](../../record.go) 的固定宽度读取；复用既有独立字节复制。没有为这个小增量建立新的类型框架或复制另一套解码函数。

## 边界

不支持 BINARY(0)、BINARY 主键、CHAR BINARY 或二进制比较 API；沿用可信 schema、新建只插入、MySQL 8.0.45/16 KiB/DYNAMIC 的表空间边界。非 NULL 固定字段的所有字节模式都合法，不能把非零尾字节当损坏，因为那可能就是原值。结构损坏、截断和非法 schema 仍明确报错；不声称识别所有错误外部元数据。
