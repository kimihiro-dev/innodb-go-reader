# 29 BIT：位宽、大端字节与完整数值

本章学习目标是恢复 BIT(1..64) 的完整值，理解声明位宽如何决定字段长度，以及为什么 BIT(1) 与 NULL 位图不能混为一谈。先读 [记录到列值](04-record-to-values.md) 和 [整数编码](09-integer-values.md)；混合列验证见 [下一章](30-bit-validation.md)。

## 输出与元数据契约

BIT 的声明参数表示有效位数，不是字节数。schema 使用独立的 `bit_length`，取值 1..64；显式要求提供，省略为零时拒绝。SQL 里省略长度的 BIT 等同 BIT(1)，但调用方仍须传入 `bit_length: 1`。

```json
{"name":"flags","type":"BIT","bit_length":9,"nullable":true,"max_chars":0}
```

非 NULL 值统一返回 Go `uint64`，包括 BIT(1)。零返回数字 0，NULL 返回 nil；不把 BIT(1) 改成 bool。位串的前导零没有丢失语义：数值和声明位宽一起可以重建，例如值 5、位宽 9 对应 `000000101`。本实现不另设位串 API。

JSON 使用整数数字，BIT(64) 最大值是 `18446744073709551615`。消费端需保留整数精度，例如 Go 的 `json.Decoder.UseNumber()`；不要先转 float64。BIT 不允许额外 unsigned、fsp、precision/scale 或字符串长度参数，主键仍限此前支持的整数类型。

## InnoDB 中的字节布局

在本项目支持的 MySQL 8.0.45 DYNAMIC 表中，BIT 是定长二进制字段：

```text
BIT(M)：ceil(M/8) 字节，大端
首字节：未使用高位必须为零 | 有效高位
后续字节：有效位，每字节八位，直到最低位
```

| 声明位宽 | 字节数 | 最大值的字节 | 首字节有效位数 |
|---|---:|---|---:|
| BIT(1) | 1 | `01` | 1 |
| BIT(7) | 1 | `7f` | 7 |
| BIT(8) | 1 | `ff` | 8 |
| BIT(9) | 2 | `01 ff` | 1 |
| BIT(15) | 2 | `7f ff` | 7 |
| BIT(16) | 2 | `ff ff` | 8 |
| BIT(31) | 4 | `7f ff ff ff` | 7 |
| BIT(63) | 8 | `7f ff ff ff ff ff ff ff` | 7 |
| BIT(64) | 8 | `ff ff ff ff ff ff ff ff` | 8 |

这些片段来自交付的 `bit_mixed` id=1。BIT(9) 的 `01 ff` 按大端计算为 `1×256+255=511`；`02 00` 虽然只占两字节，却表示 512，超过九位容量，必须拒绝。

读取时从 value=0 开始，每个字节执行 `value = value<<8 | byte`。对 M<64 检查 `value>>M==0`；M=64 接受完整 uint64 范围，不能通过计算有符号 `1<<64` 或转 int64 校验。它没有有符号 INT 的符号位翻转，也不按小端读取。

BIT 字段之间按整字节分开；两个 BIT(1) 在本范围中各占一字节，不合并成一字节中的两个 bit。BIT 的实际值也不存入 InnoDB NULL 位图。NULL 位图只决定该字段是否缺席：NULL 不占数据字节，非 NULL 的零仍占完整字段宽度。

## 真实混合记录定位

资产：[schema](../../testdata/bits/bit_mixed.json)、[压缩物理文件](../../testdata/bits/bit_mixed.ibd.gz)、[manifest 与 SHA256](../../testdata/bits/manifest.json)。id=1 的页号是 4，记录 origin=198、End=277。文件页基址是 `4×16384=65536`。

| 页内区间 | 相对 origin | 长度 | 字段 | 真实原值 |
|---|---:|---:|---|---|
| [198,202) | 0 | 4 | id | INT 主键 |
| [202,208) | 4 | 6 | DB_TRX_ID | 系统字段 |
| [208,215) | 10 | 7 | DB_ROLL_PTR | 系统字段 |
| [215,216) | 17 | 1 | b0 BIT(1) | `01` |
| [216,217) | 18 | 1 | b1 BIT(7) | `7f` |
| [217,218) | 19 | 1 | b2 BIT(8) | `ff` |
| [218,220) | 20 | 2 | b3 BIT(9) | `01 ff` |
| [220,222) | 22 | 2 | b4 BIT(15) | `7f ff` |
| [222,224) | 24 | 2 | b5 BIT(16) | `ff ff` |
| [224,228) | 26 | 4 | b6 BIT(31) | `7f ff ff ff` |
| [228,236) | 30 | 8 | b7 BIT(63) | `7f ff ff ff ff ff ff ff` |
| [236,244) | 38 | 8 | b8 BIT(64) | `ff ff ff ff ff ff ff ff` |
| [244,257) | 46 | 13 | note | UTF-8 “位字段😀” |
| [257,277) | 59 | 20 | body | 页外引用 |

例如 b3 的文件偏移为 `65536+218=65754`；b8 为 `65772`。这两个值分别恢复成 511 和最大 uint64，后续 note 仍准确从页内 244 开始。物理 id 总在前面，而输出按 schema 的逻辑列顺序还原。

## 源码与实现对应

已下载核对 MySQL 官方 mysql-8.0.45：

- [field.cc:9083–9116](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L9083) 的 `Field_bit_as_char` 使用 `(len_arg+7)/8` 字节并设置 `bit_len=0`，写入时检查首字节超出声明位宽的位、补齐左侧零字节。
- [field.cc:8772–8800](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L8772) 的 `Field_bit::val_int` 按大端的 mi_uintNkorr 还原整数。
- [ha_innodb.cc:7957](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc#L7957) 将二进制 BIT 映射为 DATA_FIXBINARY。

注意 SQL 层某些引擎会使用 `Field_bit` 的额外位区；不能把那个内存/引擎布局直接套到这里的 InnoDB 物理记录。

Go 对应 [bit.go](../../bit.go) 的 `bitWidth/decodeBit`，由 [schema.go](../../schema.go) 校验参数；[record.go](../../record.go) 先处理 NULL，再读取定长字节、解码并给错误附上列名。没有新增依赖，也没有改变整数主键的导航算法。

当前仍限定可信外部 schema、新建只插入的 8.0.45 简单表；未使用高位检查不能证明任意外部位宽都正确。例如 BIT(7) 和 BIT(8) 同样占一字节，值 1 对两者都合法。元数据自动恢复、BIT 主键和其他引擎格式另行规划。
