# 21 DATE 与 YEAR：日期分量和年份偏移

## 学习目标

读完本章，你可以从三字节 DATE 提取年月日，解释一字节 YEAR 为什么能表示 2024，并区分 SQL NULL、零日期、零年与不符合日历的日期。前置知识是第 09 章整数符号变换和第 04 章记录字段定位。

本阶段不涉及时区或小数秒。DATE/YEAR 不通过 time.Time 解码，返回存储的日期分量或年份；DATETIME、TIME、TIMESTAMP 另行实现。

## schema 与输出

| 类型 | 非 NULL 宽度 | Go 返回类型 | 示例 JSON |
|---|---:|---|---|
| DATE | 3 字节 | string | `"2024-02-29"`、`"0000-00-00"` |
| YEAR | 1 字节 | uint16 | `2024`、`0` |
| 任一类型的 NULL | 0 字节 | nil | `null` |

```json
{"name":"birthday","type":"DATE","nullable":true}
```

不填写 unsigned、precision、scale、max_chars、max_bytes；这些属性必须为零/false。YEAR 内部的无符号存储不等于 SQL 声明需要 unsigned。主键继续限单列非空整数。

YEAR 数字输出不保留 SQL 显示零年的四位补零：`0000` 表示年份值 0，JSON 输出 0。DATE 则固定保留四位年、两位月日。二者零值均不同于 NULL。

## DATE 的两层转换

服务器缓冲中的年月日先打包为整数：

```text
packed = (year << 9) | (month << 5) | day
```

日占低五位、月占接下来四位、年占更高位。服务器 Field_newdate 从小端三字节缓冲中读出这个数，但这还不是最终 InnoDB 磁盘形式。

DATE 对应 InnoDB DATA_INT。写入 InnoDB 时转换为大端，并翻转有符号字段的最高位。因此文件中的三个字节要这样还原：

```text
u = 三字节大端整数 XOR 0x800000
year  = u >> 9
month = (u >> 5) & 15
day   = u & 31
```

这里应同时阅读官方 [Field_newdate::get_date_internal](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L5690)、[InnoDB 类型映射](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc#L7967) 和 [DATA_INT 转换](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/row/row0mysql.cc#L415)。只看到服务器函数使用 uint3korr，就把 `.ibd` 当小端读取，会得出错误结果。

当前校验年不大于 9999、月不大于 12。日通过五位字段提取，范围自然是 0..31。错误的符号编码也会导致年超出支持范围；所有这些结构错误返回 ErrCorrupt。校验分量不等于检查某年某月有多少天。

## 真实 DATE 字节演算

`testdata/dates/date_values.ibd.gz` 解压 SHA256 为 `b1df4ee1049bb7d21ad466669c8d80cf4e1167b3e95902ff74ad415e44997be4`。表为 id INT 主键加可空 DATE value，所有记录在页 4。id=9 的 origin=357，value 起点为 `357+4+13=374`，文件绝对偏移为 `4×16384+374=65910`。

```text
页内 374..377：8f d0 5d
大端整数：    0x8fd05d
翻转最高位：  0x0fd05d
```

分量为：year=2024、month=2、day=29，输出 `"2024-02-29"`。它的三字节编码既不是 ASCII 日期，也不是距某个纪元的天数。

更多真实值如下；偏移为页内十进制，字段仍从 origin+17 开始：

| id | origin | 字节 | 返回值 |
|---:|---:|---|---|
| 0 | 126 | 无，NULL 位图为 01 | nil |
| 1 | 149 | `800000` | `0000-00-00` |
| 2 | 175 | `800021` | `0000-01-01` |
| 3 | 201 | `800221` | `0001-01-01` |
| 7 | 305 | `8ed85d` | `1900-02-29` |
| 10 | 383 | `8fce5f` | `2023-02-31` |
| 11 | 409 | `8fd00f` | `2024-00-15` |
| 12 | 435 | `8fd0a0` | `2024-05-00` |
| 13 | 461 | `ce1f9f` | `9999-12-31` |

零日期的实际值字节为 `80 00 00`，不是三字节全零。全零原始字节经过符号还原不代表合法零日期，测试要求报错。

## YEAR：零值独立，其他值加 1900

YEAR 的一个字节 b 按下列规则恢复：

```text
b == 0 → year = 0
b != 0 → year = 1900 + b
```

所以 `01` 表示 1901，`7c` 表示 2024，`ff` 表示 2155；不是 signed TINYINT，也不对该字节翻转最高位。官方 [Field_year::store/val_int](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc#L5588) 定义年份偏移；夹具 year_values 对全部 256 个字节及一个 NULL 逐行核验。所有字节都对应一种 YEAR 值，没有额外可用的非法字节码；截断仍属于错误。

## SQL 范围与物理还原范围

MySQL 手册通常保证的 DATE 范围为 1000-01-01..9999-12-31，并说明更早日期可能工作但不保证；实际允许的零日期、部分零日期和非法日历日期受 SQL 模式影响。见 [DATE 类型手册](https://dev.mysql.com/doc/refman/8.0/en/datetime.html) 与 [YEAR 类型手册](https://dev.mysql.com/doc/refman/8.0/en/year.html)。

本项目针对交付的 8.0.45 样本，按物理分量支持年 0..9999、月 0..12、日 0..31；这不是扩大 MySQL 官方 SQL 保证范围。新夹具使用专用会话的 ALLOW_INVALID_DATES，因此 1900-02-29、2023-02-31 可实际保存。解析器不根据读取时的 SQL 模式重审历史值，也不使用 time.Date 将其归一化为别的日期。

代码入口为 `date.go/dateWidth/decodeDate`、`schema.go` 和 `record.go`。下一章把日期放回混合记录，解释 NULL 位图和完整验证过程。
