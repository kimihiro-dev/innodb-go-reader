# 19. FLOAT / DOUBLE：按位还原近似数值

<a id="学习目标"></a>

## 学习目标与前置概念

本章解释 FLOAT/DOUBLE 为什么使用小端、如何区分实际存储值和显示小数，以及怎样还原负零、次正规数和精度边界。前置知识是第 09 章整数编码、第 17 章 DECIMAL，以及记录 origin 后先存主键和系统字段的规则。

## 类型与返回约定

| SQL 规范类型 | 物理字节数 | Go 返回类型 | JSON |
|---|---:|---|---|
| FLOAT | 4 | float32 | 数字 |
| DOUBLE | 8 | float64 | 数字 |

两者均支持 NULL 和 UNSIGNED；当前只接受有限值，非有限的 NaN/Infinity 编码报 ErrCorrupt。UNSIGNED 的非零负值报错；零的符号按文件保留。浮点主键仍不支持。

schema 示例：

```json
{"name":"measurement","type":"FLOAT","nullable":true}
```

precision/scale/max_chars/max_bytes 必须为零。这里的 precision 仍专属于 DECIMAL，不能用来写 SQL FLOAT(p)。调用者须提供经过核对的规范 FLOAT/DOUBLE 类型；SQL 别名、带 M,D 的旧声明及 DDL 解析不在本阶段范围。

MySQL 浮点类型是近似数值，存储宽度分别为四和八字节，相关 SQL 背景见 [MySQL 8.0 浮点类型手册](https://dev.mysql.com/doc/refman/8.0/en/floating-point-types.html)。解析器恢复的是已存储的位模式，无法从舍入后的文件逆推出插入前的精确小数。

## 不同类型不能共用字节序规则

InnoDB 整数使用大端，有符号整数还翻转最高符号位；DECIMAL 将十进制数字分组并进行符号变换；FLOAT/DOUBLE 直接使用小端 IEEE 754。不能把前两种算法套在浮点字段上。

官方 8.0.45 源码 [mach0data.ic:550–630](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/mach0data.ic#L550) 中，mach_float_read/mach_double_read 明确从小端序读取，写入函数对应保存同样的布局。

```text
文件中：低有效字节 → 高有效字节
读取后 32/64 位整数： [符号][指数][尾数字段]
FLOAT：                  1     8      23 位
DOUBLE：                 1    11      52 位
```

设符号位为 s、指数域为 E、尾数字段为 F，尾数位数为 t，指数偏置分别为 127 和 1023：

- 正规数：`(-1)^s × (1 + F/2^t) × 2^(E−偏置)`。
- E=0、F≠0 是次正规数：`(-1)^s × (F/2^t) × 2^(1−偏置)`。
- E=0、F=0 是零，s 可保留正负区别。
- 指数域全 1 的编码不属于当前有限值契约。

实际代码不手写幂运算：`float.go/decodeFloat` 用 binary.LittleEndian 读位，再用 math.Float32frombits/Float64frombits 解释，并进行有限性与 unsigned 检查。这避免通过十进制文本重新近似计算存储值。

## 真实字节：0.1 与显示值

`testdata/floating/float_values.ibd.gz` 解压后 SHA256 为 `2af30ee43f0e984923406bd22d37d676f3f63138f1f5a23a1ed9a87ae7ccc40c`。列顺序为 id INT、value FLOAT、positive FLOAT UNSIGNED，后两列可空。

id=140 在页 4、origin=4466。value 从页内 `4466+4+13=4483` 开始，文件绝对偏移为 `4×16384+4483=70019`。四字节为：

```text
cd cc cc 3d  → 小端整数 0x3dcccccd
```

其符号为 0，指数域为 123，F=5033165。代入正规数公式得到实际值 `0.100000001490116119384765625`，并非精确十进制 0.1。Go float32 的标准 JSON 可显示为 `0.1`，这是能往返恢复同一个 float32 的短表示，不表示存储值变成了精确小数。

相应 `double_values` 中 id=140 的 origin=5586，value 从 5603 开始，八字节为：

```text
9a 99 99 99 99 99 b9 3f  → 0x3fb999999999999a
```

它比 FLOAT 更接近十进制 0.1，但仍属于近似表示。SQL JSON 对 FLOAT 输出 `0.10000000149011612`；测试将该文本按 32 位浮点解析，再比较位模式，而不是要求 SQL 与 Go JSON 的字符串形式相同。

## 真实边界与零

以下均来自 float_values 页 4，value 起点为 origin+17：

| id | origin | 四字节（地址递增） | 含义 |
|---:|---:|---|---|
| 0 | 126 | `00000000` | 正零 |
| 1 | 157 | `00000080` | 负零 |
| 2 | 188 | `01000000` | 最小正次正规数，2^-149 |
| 4 | 250 | `ffff7f00` | 最大正次正规数 |
| 5 | 281 | `00008000` | 最小正规数，2^-126 |
| 6 | 312 | `ffff7f7f` | 最大有限正值 |
| 141 | 4497 | `0000804b` | 16777216 |

id=141 的生成输入为 16777217，经 FLOAT 存储后成为 16777216。在这个量级相邻可表示数的间隔已经大于一，解析器返回存储后的值才是正确结果。DOUBLE 对 9007199254740993 也有类似边界：夹具 id=141 的位模式为 `0x4340000000000000`，还原为 9007199254740992。

负零在 SQL 数值比较中等于正零，但物理符号位不同；本项目保留它，位模式测试不能只用 `a == b`。DOUBLE 最小次正规数的真实字节也为 `01` 后接七个零，即 2^-1074。

## 记录定位与边界

`schema.go` 校验属性，`floatWidth` 决定四或八字节，`record.go` 在 NULL 判断后读取固定长度，`decodeFloat` 返回对应原生类型。没有变长长度项，不走 LOB 分支，输入切片不会被修改。

标准 JSON 往返需要按列类型读取：FLOAT 用 32 位，DOUBLE 用 64 位；不可将 FLOAT 的短 JSON 表示当成存储值提升为 float64 后应有的精确文本。DECIMAL 则继续返回字符串，二者的精度契约不同。下一章把浮点字段放回混合记录并给出验证方法。
