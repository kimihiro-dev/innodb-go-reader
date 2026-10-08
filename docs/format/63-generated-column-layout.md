# 63. 生成列、不可见列与物理字段

学习目标：区分表达式、存储结果和SQL可见性，解释为何VIRTUAL不占聚簇载荷，并从真实记录解码STORED生成列。

前置：[04 记录到列值](04-record-to-values.md)、[43 SDI到schema](43-sdi-schema.md)、[61 INSTANT行布局](61-instant-row-layout.md)。本章样本为 `testdata/generated/` 的MySQL8.0.45快照；偏移均为页内偏移，文件绝对偏移为 `页号×16384+页内偏移`。

<a id="1-三个概念不能混用"></a>

## 三个概念不能混用

| 用户列 | 聚簇行中的值 | 本阶段输出 |
|---|---|---|
| 普通列 | 有，NULL用位图表示 | 按类型恢复 |
| STORED生成列 | 有，数据库写入时计算并保存 | 恢复保存的结果，不重算表达式 |
| VIRTUAL生成列 | 没有完整聚簇字段 | 单独报告未物化，不返回假NULL |
| 用户INVISIBLE列 | 取决于是否VIRTUAL；INVISIBLE本身不取消存储 | 物化列仍返回，保留Invisible属性 |

STORED结果本身可以是SQL NULL。VIRTUAL表达式即使在SQL查询时恰好得到NULL，也不是文件解码器已经读出了NULL。普通 `SELECT *` 不列出用户不可见列，因此验收必须指定列名。

本地官方源码 `sql/dd/types/column.h:95–106` 定义hidden：1为普通可见，2为存储引擎隐藏，3为SQL内部隐藏（例如函数索引），4为用户INVISIBLE。2不等于4，不能把不可见用户列当作系统字段或已DROP列。`storage/innobase/dict/dict0dd.cc:3385–3399` 在物理列遍历中跳过VIRTUAL和系统列；同文件2986–2997对虚拟列索引设置DICT_VIRTUAL等标志。

SDI的 `is_virtual` 与 `generation_expression` 用于分类：生成表达式非空且is_virtual=false表示STORED；is_virtual=true的用户列必须有表达式。表达式只作为元信息保存，不调用SQL、不尝试在Go里执行。

<a id="2-从声明列到存储列"></a>

## 从声明列到存储列

真实 `mixed_initial` 的声明顺序和返回映射如下：

| SQL序号（1起） | 列名 | 类别 | Values下标（0起） |
|---:|---|---|---:|
| 1 | id | 不可见BIGINT UNSIGNED主键 | 0 |
| 2 | a | 普通INT | 1 |
| 3 | virt | VIRTUAL(a+1) | 无 |
| 4 | txt | 普通LONGTEXT | 2 |
| 5 | stored_n | 不可见STORED(a*2) | 3 |
| 6 | stored_text | STORED(CONCAT(txt,txt)) | 4 |
| 7 | secret | 不可见VARBINARY | 5 |
| 8 | vt | 不可见VIRTUAL(LEFT(txt,3)) | 无 |

```text
SDI用户列：id a virt txt stored_n stored_text secret vt
                    │                              │
                    └──VirtualColumns（未物化）────┘
                            ↓ 只按物化字段定位
聚簇字段：id | DB_TRX_ID | DB_ROLL_PTR | a | txt | stored_n | stored_text | secret
输出列序：id                           a   txt   stored_n   stored_text   secret
```

`VirtualColumn.Ordinal`是当前用户SQL列序号，排除系统列和已DROP列；`IndexElement.Column`仍是原始SDI数组下标，两者不能混用。`Schema.Columns`仅包含物化列，按它们在SQL声明中的相对顺序排列。键字段先存储的既有规则保持。

`mixed_initial` 的SDI聚簇elements恰好引用0、8、9、1、3、4、5、6：8/9是事务系统列；没有引用2/7的两个VIRTUAL列。解析器核对完整字段列表，不能看到is_virtual就跳过而不核对索引布局。

<a id="3-真实stored字节逐步解码"></a>

## 真实STORED字节逐步解码

`stored_values_initial` 首行位于页4，Start120、origin129、End172，文件数据起点为 `4×16384+129=65665`。

```text
页内120: 02 09 07 | 00 | 00 00 10 00 31
           长度    NULL       固定记录头
页内129: 80 00 00 01
页内133: 00 00 00 00 05 a5
页内139: 81 00 00 00 b2 01 10
页内146: 80 00 00 07
页内150: e7 95 8c f0 9f 98 80
页内157: 80 00 00 0e
页内161: e7 95 8c f0 9f 98 80 3a 37
页内170: 00 ff
```

| 页内范围 | 长度 | 字段及含义 |
|---|---:|---|
| [120,123) | 3 | 反向长度数组：secret=2、label=9、text=7 |
| [123,124) | 1 | 当前行各可空物化列均非NULL |
| [124,129) | 5 | 记录头；不是生成列标记 |
| [129,133) | 4 | id=1，有符号INT大端、最高位翻转 |
| [133,146) | 13 | 原始事务ID六字节与回滚指针七字节 |
| [146,150) | 4 | a=7 |
| [150,157) | 7 | text的UTF-8字节，值为“界😀” |
| [157,161) | 4 | twice=14，STORED(a*2)的实际整数编码 |
| [161,170) | 9 | label=“界😀:7”，STORED字符串的实际UTF-8字节 |
| [170,172) | 2 | 不可见secret，完整二进制00ff |

1. 从NULL位图确认要消费哪些载荷和长度字节。
2. 按物理列序读取text、label、secret对应的变长长度。
3. twice直接由 `8000000e` 解码为14；不读取表达式来计算7×2。
4. label使用已验证的文本解码器，secret仍为独立 `[]byte{0,255}`。
5. 第二条真实行的a/text为空，数据库保存的twice/label也是NULL；这由它们各自的物理NULL位确认。

修改a/text之后，`stored_values_updated_visible` 的SQL与物理STORED值同步变化；将secret改为VISIBLE只改变列属性，解析器仍返回它。不能因为SELECT *的显示范围改变而调整物理字段长度。

<a id="4-页外生成值与instant"></a>

## 页外生成值与INSTANT

`mixed_initial` 首行有两个页外字段：txt与stored_text。前者为42,000字节，后者为84,000字节；两列各自有20字节引用、各自的LOB活动块来源。存储的重复文本通过LOB路径恢复，不通过CONCAT生成。记录头前的实际字节是：

```text
02 14 c0 14 c0 | 00 | 00 00 10 00 4d
反向长度：2、external20、external20    NULL   固定头
```

origin131，End202；BIGINT主键八字节。VIRTUAL没有长度项，也不增加NULL位。

`mixed_instant` 在最前添加added、最后添加tail，旧行仍保留上述本地布局；其输出 `DefaultColumns=[0,7]` 指向物化Columns中的added和tail。SDI将它们分配在完整聚簇physical_pos8和9。VIRTUAL仅有table_id，没有physical_pos，不能要求每一个SDI声明列都提供INSTANT物理位置。

`mixed_changed` DROP不可见secret并更新基础列及页外值；旧DROP字段仍由阶段29逻辑定位，当前Columns中不再出现它。`mixed_rebuilt`清除行版本历史。最后DROP两个VIRTUAL后的 `mixed_no_virtual` 可再次通过严格ReadAuto读取完整行。

代码入口：`metadata.go`负责列分类、原SDI下标与完整聚簇布局；`instant.go:metadataInstant`跳过VIRTUAL，保留原物理位置校验；`record.go`和`lob.go`继续解码实际载荷。入口契约、NULL位图和验收见[第64章](64-generated-validation.md)。
