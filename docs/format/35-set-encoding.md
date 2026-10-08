# 35 SET：成员字典与位掩码

本章目标：从 SET 的整数位掩码恢复选中的成员，理解它与 ENUM、BIT 的区别，并保留仅靠显示字符串无法表达的状态。前置知识见 [BIT](29-bit-encoding.md)、[ENUM](33-enum-encoding.md) 和 [记录列值](04-record-to-values.md)。

## 一个序号与多个选择

ENUM 的整数选择一个字典位置；SET 的每一位选择一个成员。字典 `['red','green','blue']` 中，掩码 5（二进制 101）选中 red 和 blue；零掩码表示空集合，NULL 则是字段不存在。

schema 使用独立 `set_values`，按实际 MySQL 字典顺序提供 1..64 个 UTF-8 标签，禁止标签内含逗号，允许空标签。它不接受 ENUM 的 enum_values 或其他类型参数；字典 SQL 解析、DDL 规范化与 collation 校验仍由调用方负责。

```json
{"name":"colors","type":"SET","set_values":["red","green","blue"],"nullable":true,"max_chars":0}
```

Values 返回 SQL 风格字符串，SetMasks 保存物理掩码。其键为 Schema.Columns 的零基逻辑列下标，值为 uint64；只给非 NULL SET 添加键。

| 状态 | Values | SetMasks |
|---|---|---|
| NULL | nil | 无该列键 |
| 空集合 | `""` | 该列 → 0 |
| 选中 red/blue | `"red,blue"` | 该列 → 5 |
| 第 64 项被选中 | 相应标签 | 该列掩码的最高位为 1 |

调用方必须使用 `mask, present := rec.SetMasks[columnIndex]` 判断是否存在，不能把 map 缺失键的默认零值当空集合。uint64 可完整保存最高位及全一值；JSON 数字消费端需使用 UseNumber 或其他精确整数方式。

## 固定宽度不是简单的 ceil(N/8)

MySQL SET 的固定存储宽度只取 1、2、3、4、8 字节，没有 5、6、7 字节格式。这一点不同于 BIT。官方 [存储需求](https://dev.mysql.com/doc/refman/8.0/en/storage-requirements.html?ff=nopfpls) 规定按成员数计算后向这些宽度取整。

| 成员数 N | 固定字节数 | 最大合法掩码示例 |
|---|---:|---|
| 1..8 | 1 | N=8：ff |
| 9..16 | 2 | N=9：01 ff |
| 17..24 | 3 | N=17：01 ff ff |
| 25..32 | 4 | N=25：01 ff ff ff |
| 33..64 | 8 | N=33：00 00 00 01 ff ff ff ff |

在 InnoDB 页中，字段按大端无符号整数读取，不翻转符号位。逻辑最低位对应第一个字典成员，因此它位于物理字段的最后一字节。不能按文件从左到右的位顺序直接依次选择字典成员。

```text
SET(64 项) 的字段：80 00 00 00 00 00 00 01
                   ↑                    ↑
                第64项                第1项
掩码 = 2^63 + 1
```

N<64 时检查 `mask>>N==0`，拒绝选中不存在成员的高位；N=64 则所有掩码都可表示。特别是 SET(33) 虽占八字节，仍有 31 个未使用高位必须零。

## 空标签为何需要保留原掩码

交付的 set_labels 分别使用 `['','a','b']`、`['a','','b']`、`['a','b','']`。三个字典的全部选中掩码均为 7，但 SQL 显示不同：

| 字典 | SQL 显示 |
|---|---|
| 空标签在首位 | `a,b` |
| 空标签在中间 | `a,,b` |
| 空标签在末尾 | `a,b,` |

MySQL Field_set::val_str 遍历选中的位，每追加一个标签前，只有输出缓冲已经非空才插入逗号。因此不能简单对所有被选中标签调用 strings.Join：它会给第一种情况多加一个前导逗号。

只选首位空标签（掩码 1）与空集合（掩码 0）都显示为空字符串；原始掩码保证这两个状态可区分。字段 NULL 则无掩码项。SQL 输入成员的先后顺序和重复次数不会原样保存，解析器只恢复存储后的集合及其 SQL 显示。

## 真实 SET(33) 与 SET(64) 字段

资产：[set_mixed schema](../../testdata/sets/set_mixed.json)、[物理文件](../../testdata/sets/set_mixed.ibd.gz)、[manifest](../../testdata/sets/manifest.json)。id=1 的页为 4，origin=199，数据区 End=279，页基址为 65536。

| 页内区间 | 相对 origin | 内容与字节 |
|---|---:|---|
| [199,216) | 0 | INT id 与两个系统字段，共 17 字节 |
| [216,217) | 17 | s0，一项：01 |
| [217,218) | 18 | s1，八项：ff |
| [218,220) | 19 | s2，九项：01 ff |
| [220,222) | 21 | s3，十六项：ff ff |
| [222,225) | 23 | s4，十七项：01 ff ff |
| [225,228) | 26 | s5，二十四项：ff ff ff |
| [228,232) | 29 | s6，二十五项：01 ff ff ff |
| [232,240) | 33 | s7，三十三项：00 00 00 01 ff ff ff ff |
| [240,248) | 41 | s8，六十四项：ff ff ff ff ff ff ff ff |
| [248,249) | 49 | ENUM state：03 → 中文😀 |
| [249,259) | 50 | note：十字节 UTF-8 “集合😀” |
| [259,279) | 60 | body：20 字节页外引用 |

s7 的文件偏移为 `65536+232=65768`，取八字节解为 `8589934591=2^33−1`，依字典顺序输出 v0..v32。若只取五字节，后续 SET、ENUM、文本和页外引用都会错位。s8 的值为完整 uint64 最大值 18446744073709551615，不可经 int64 或 float64 中转。

九个 SET 总长 32 字节，数据区 `17+32+1+10+20=80`，与 End−origin 一致。主键逻辑上位于前四个 SET 之后，但物理上提前；SetMasks 仍使用逻辑列下标。

## 源码与实现

已核对官方 mysql-8.0.45 标签的 [ha_innodb.cc:7932](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc#L7932)：SET 与 ENUM 映射 DATA_INT/DATA_UNSIGNED；[field.cc:8380–8408](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L8380) 的 Field_set::val_str 定义按位选取和缓冲长度分隔规则。

[set.go](../../set.go) 的 setWidth/decodeSet 用整数位操作和 strings.Builder；[schema.go](../../schema.go) 验证独立字典及参数；[record.go](../../record.go) 在 NULL 判断后固定取值，填充 Values 与 SetMasks。没有新增依赖，也不改变 ENUM 原始序号或整数主键行为。

本阶段仍要求可信字典；同长度错误字典无法仅从位掩码发现。SET 主键、自动元数据、排序规则、历史事务/ALTER 布局均未扩展。完整测试见 [下一章](36-set-validation.md)。
