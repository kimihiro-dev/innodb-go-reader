# 33. ENUM：从物理序号到标签

本章学习目标是区分 ENUM 标签与记录中的整数编码，按字典还原业务值，同时保留零号错误值和合法空标签的差异。先读 [整数编码](09-integer-values.md) 与 [记录列值](04-record-to-values.md)；完整实验见 [下一章](34-enum-validation.md)。

## 字典在元数据中，记录只存序号

例如 ENUM('','ready','中文😀','2')，记录中的非 NULL 值是 0..4 的序号，不是标签的 UTF-8 字节。序号 1 对应第一个声明成员，序号 0 是特殊错误值。这里的“序号”指字典位置，与 B+ 树索引不是同一个概念。

schema 新增有序 `enum_values`：

```json
{"name":"status","type":"ENUM","enum_values":["","ready","中文😀","2"],"nullable":true,"max_chars":0}
```

字典必须是实际建表后、经过 SQL 规范化的标签列表，顺序不可改变。本实现检查成员数 1..65535 和 UTF-8，使用可信元数据，不负责 SQL 转义、去除 DDL 尾空格、collation 比较或自动发现字典。原始 CREATE TABLE 文本不能未经解析直接当成列表使用。

| 存储状态 | Values 中的值 | EnumIndexes 中的项 |
|---|---|---|
| NULL | nil | 无该列键 |
| 错误值，序号 0 | `""` | 该列 → 0 |
| 合法空标签，序号 1 | `""` | 该列 → 1 |
| 序号 2 | `"ready"` | 该列 → 2 |
| 序号 3 | `"中文😀"` | 该列 → 3 |
| 序号 4 | `"2"` | 该列 → 4 |

[MySQL 的 ENUM 说明](https://dev.mysql.com/doc/refman/8.0/en/enum.html) 定义了从 1 开始的成员序号、错误值 0 和 NULL 的区别。字符串输出本身不能区分表中第二、三行，因此 `Record.EnumIndexes` 保留每个非 NULL ENUM 的原始 uint16 序号；键为 Schema.Columns 的零基逻辑列下标。

```go
label := rec.Values[columnIndex]
ordinal, present := rec.EnumIndexes[columnIndex]
// 已知该列为 ENUM：present=false 表示 NULL；present=true 且 ordinal=0 是错误值。
// 不能只写 ordinal := rec.EnumIndexes[columnIndex]，否则缺失键也得到 0。
_ = label
_ = ordinal
_ = present
```

既有 Values 仍是字符串/基础类型，旧表没有 ENUM 时 map 为 nil。示例程序只打印 Values，输出与 SQL 标签一致；需要无损区分零号和空标签时使用完整 Read API 的 EnumIndexes。

## 一字节和两字节的边界

| 字典成员数 N | 固定字段宽度 | 合法物理序号 |
|---|---:|---|
| 1..255 | 1 字节 | 0..N |
| 256..65535 | 2 字节 | 0..N |

宽度取决于字典成员数，而不是当前值的大小或标签字节长度。65535 项字典中即使值为 1，也要读两字节 `00 01`。NULL 不占值字节，非 NULL ENUM 不参与变长长度数组。

```text
ENUM(256 项)：字段起点 ├─ 高八位 ─┼─ 低八位 ─┤ 下一列
                             01         00
                         (1 << 8) | 0 = 256 → 字典第 256 项
```

InnoDB 页中是大端无符号整数，没有普通有符号整数的最高位翻转。`80 00` 是 32768，不是负数。超过字典范围的序号属于可检测损坏，返回 ErrCorrupt；序号零则是有定义的存储状态，不能误判为损坏。

## 真实大字典边界

[enum_65535 schema](../../testdata/enums/enum_65535.json) 的标签为 v1..v65535，真实文件及哈希见 [manifest](../../testdata/enums/manifest.json)。根页号 4，页基址 65536。以下是交付文件实际记录：

| id | origin | 字段页内偏移 | 字段字节 | 序号 | 标签 |
|---:|---:|---:|---|---:|---|
| 0 | 126 | 无 | NULL | 无 | nil |
| 1 | 149 | 166 | `00 00` | 0 | 空字符串 |
| 2 | 174 | 191 | `00 01` | 1 | v1 |
| 3 | 199 | 216 | `00 ff` | 255 | v255 |
| 4 | 224 | 241 | `01 00` | 256 | v256 |
| 5 | 249 | 266 | `7f ff` | 32767 | v32767 |
| 6 | 274 | 291 | `80 00` | 32768 | v32768 |
| 7 | 299 | 316 | `ff ff` | 65535 | v65535 |

每个字段都在 origin+17：前面四字节 INT 主键、六字节 DB_TRX_ID、七字节 DB_ROLL_PTR。id=4 字段文件偏移为 `65536+241=65777`，取 `01 00`，解为 256，然后查询 `enum_values[255]` 得到 v256。非 NULL 数据区长 19，NULL 行长 17，记录头和位图另计。

标签较长不会使用户行字段变长。例如 enum_labels 的 id=4 标签“中文😀”占十个 UTF-8 字节，但页内 238 只存一字节 `03`。字典来自元数据；本阶段没有解析 SDI，因此调用方必须提供它。

## 源码核对及代码入口

已核对 MySQL 官方 mysql-8.0.45：

- [ha_innodb.cc:7932–7942](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc#L7932) 将 ENUM/SET 明确作为 DATA_INT，设置 DATA_UNSIGNED。
- [field.cc:8217](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L8217) 的 Field_enum::val_int 读取 SQL 层缓冲；其字节序参数属于该缓冲布局，不应直接套用到 .ibd。
- [field.cc:8239–8248](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L8239) 的 val_str 将序号映射到 typelib 标签，并对零返回空字符串。我们的范围校验更严格：超出可信字典的物理序号直接报错，不静默输出空字符串。

[enum.go](../../enum.go) 实现宽度计算、原始序号读取和字典查询；[schema.go](../../schema.go) 验证元数据；[record.go](../../record.go) 负责 NULL、定长取值和 EnumIndexes 记录。没有新增依赖或改动主键比较算法。

本阶段仍不支持 ENUM 主键、SET、ALTER/INSTANT 历史布局、SQL 输入转换或排序 API。相同长度字典的错误排序无法仅靠页字节检测，不能把可信 schema 契约描述成自动校验能力。
