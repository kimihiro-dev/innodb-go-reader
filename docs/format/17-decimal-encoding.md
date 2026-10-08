# 17. DECIMAL：从定长二进制到精确小数

## 学习目标与前置概念

本章解释 DECIMAL 的 precision/scale、固定存储长度、九位十进制分组和符号变换，并用真实文件还原正负小数。先读第 04 章的 NULL 与字段布局、第 09 章的整数编码、第 11 章的变长长度区。

DECIMAL 使用另一套数值编码。不能将它当 IEEE 浮点数，也不能直接套用普通整数的解码。当前 API 返回保留声明小数位的 string，例如 `"1234567890.1234"`、`"0.0000"`；无需先转换为浮点数。

## precision/scale 与 schema

`DECIMAL(P,S)` 的 P 是总十进制位数，S 是小数位数，整数部分最多 P−S 位。当前支持 `1≤P≤65`、`0≤S≤min(P,30)`，包括 UNSIGNED 和 NULL。

```json
{"name":"amount","type":"DECIMAL","precision":14,"scale":4,"nullable":true}
```

precision 必须显式提供；scale 省略代表 0。max_chars/max_bytes 必须为零，非 DECIMAL 类型不得携带非零 precision/scale。主键仍只支持单列非空整数，DECIMAL 只用于普通列。声明别名及 SQL DDL 解析不属于本阶段。

`DECIMAL(14,4)` 的整数最多十位、小数四位；`DECIMAL(30,30)` 没有整数存储组，但显示时仍以 `0.` 开头。UNSIGNED 不改变字节宽度，解码后需要额外验证值非负。

## 存储长度怎样计算

整数与小数部分分别分组，每九个十进制数字占四字节。不满九位的组使用下面的宽度；整数残组位于整数部分最前，小数残组位于小数部分最后。

| 残组位数 | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 字节数 B | 0 | 1 | 1 | 2 | 2 | 3 | 3 | 4 | 4 |

令 I=P−S，则：

```text
宽度 = 4×floor(I/9) + B[I%9] + 4×floor(S/9) + B[S%9]
```

宽度只取决于声明，和当前数值大小无关。DECIMAL(14,4) 为 `4+1+2=7` 字节，DECIMAL(65,30) 为 `12+4+12+2=30` 字节。NULL 不占值字节；非 NULL 的零仍占完整宽度。DECIMAL 没有变长长度项，即使同一记录还包含 VARCHAR 或页外 TEXT，也应分别解释。

物理排列示意：

```text
[整数残组][整数九位组…][小数九位组…][小数残组]
                 各组内部均按大端读取
```

## 符号还原与范围验证

读取原始首字节最高位：1 表示非负，0 表示负。先翻转首字节的 0x80 位；若原值为负，再将所有字节逐位取反。之后每个组按无符号大端整数解释。

对 d 位组必须验证 `0≤组值<10^d`，九位完整组必须小于 1000000000。不能因为组值装得进四字节就认为合法。解码后整数部分移除无意义的前导零，小数部分严格补足 S 位；所有组都为零时统一输出非负零。UNSIGNED 的非零负值报 ErrCorrupt。

`decimal.go` 使用 uint32 读取单组、strconv/strings 拼接十进制文本，不需要 65 位数的整数容器，也不做小数运算。这样既避免 float64 舍入，也避免用 int64 承载完整值造成溢出。官方格式依据为 MySQL 8.0.45 [decimal_bin_size](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/strings/decimal.cc#L1621)、[decimal2bin](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/strings/decimal.cc#L1353) 和 [bin2decimal](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/strings/decimal.cc#L1489)。

## 真实字节：1234567890.1234

夹具 `testdata/decimal/decimal_lesson.ibd.gz` 解压后 SHA256 为 `4dda83dc52ea6c9129fd37883db026dc7e46af4fc85b1be5252398db92e49aa3`。页 4、origin=127 的记录 id=1；主键在 schema 中声明于第三列，物理上仍排在最前。

amount 从页内 `127+4+13=144` 开始，七字节为：

```text
81 0d fb 38 d2 04 d2
```

对应文件绝对偏移 `4×16384+144=65680`。

1. `81` 最高位为 1，数值非负；翻转 0x80 后得到 `01`。
2. P−S=10，拆成一位整数残组和一个九位组，小数部分四位。
3. `01` → 1；`0d fb 38 d2` → 234567890；`04 d2` → 1234。
4. 拼接整数 `1` + 九位 `234567890`，再加小数 `.1234`，得到精确字符串。

id=2 的 amount 位于页内 184，原始字节为：

```text
7e f2 04 c7 2d fb 2d
```

首位为零，表示负数。首字节翻转后为 fe，再对所有字节取反，得到 `01 0d fb 38 d2 04 d2`；分组结果同上，添加负号即为 `-1234567890.1234`。它不是对整个数做二进制补码加一。

id=3 的零为 `80 00 00 00 00 00 00`，返回 `"0.0000"`。同一表的 DECIMAL(2,2) 用一字节：id=1 的 `81` 表示 0.01，id=2 的 `1c` 经过符号还原得到 99，表示 -0.99。

## 从记录定位到输出

`schema.go` 校验声明；`decimalWidth` 计算宽度；`record.go/decodeRecord` 在 NULL 判断后读取定长字段；`decodeDecimal` 验证并还原字符串。不存在页外 DECIMAL 分支，也不添加无用长度项。

JSON 输出中的精确小数是字符串，整数主键仍是 JSON 数字。消费者可以按自己的计算需求选择小数库；本解析器不把还原任务扩展为算术 API。下一章逐字节解释混合记录并给出完整验证方法。
