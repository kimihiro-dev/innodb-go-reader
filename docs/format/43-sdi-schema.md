# 43. 从 SDI 列定义生成 schema

本章承接 [SDI 提取](41-sdi-records.md)。此前已经能够解压出 JSON，但读用户记录仍需手工提供列定义。本章将二者连接起来：从受支持表的 SDI 生成与既有 `Schema` 完全一致的布局，再交给已有记录解码器。

这里的 schema 是**解码参数**，不是完整 SQL 数据字典。默认值、注释、权限等不影响当前存储值的还原；生成列、INSTANT 列版本等会改变解码含义，不能忽略后继续读。

## 从字节到逻辑列

```text
页0的SDI入口 → SDI树 → zlib载荷 → Table和Tablespace对象
                                     │
                         columns中的DD类型和属性
                                     ↓
                   当前支持矩阵检查 + 物理字段顺序检查
                                     ↓
                       Schema（无任何Issues时才生成）
                                     ↓
                         原Read → 用户记录与来源
```

SDI JSON 是变长文本；`type` 等键在 JSON 中**没有固定字节偏移**。固定页偏移、记录相对偏移属于前两章解释的载体，JSON 解压后才按键读取。不能用搜索原 `.ibd` 中的字符串代替 zlib 解压和 JSON 解析。

依据为用户提供的官方 MySQL 8.0.45 源码根 `/Users/kimihiro/workspace/codespace/cpp/mysql-8.0.45`：

- `sql/dd/types/column.h:53–86`：DD 类型编号。
- `sql/dd/dd_table.cc:561–580`：char_length、精度、NULL 与 unsigned 的设置。
- `sql/dd/impl/types/column_impl.cc:336–390`：列对象的 SDI 序列化。
- `sql/dd/impl/types/column_type_element_impl.cc:117–130` 和 `sql/dd/impl/sdi_impl.h:377–386`：字典成员使用 Base64。

本实现使用标准库 `encoding/json`、`encoding/base64`、`strconv`；现有项目没有能直接生成此 schema 的依赖。选择直接映射结构化元数据，不新增 SQL 解析器，也不解析面向显示的 `column_type_utf8`。

## 不能混用两套类型编号

DD 的枚举从 DECIMAL=1 开始，和协议/Field 中的 MYSQL_TYPE 数字不同。例如本章真实样本的 INT 是 DD type=4；不能直接把4当作 MYSQL_TYPE_FLOAT。

| DD type | 当前目标类型 | 必要的补充信息 |
|---|---|---|
| 2 / 3 / 4 / 9 / 10 | TINYINT / SMALLINT / INT / BIGINT / MEDIUMINT | is_unsigned、is_nullable |
| 5 / 6 | FLOAT / DOUBLE | is_unsigned；存储宽度由类型决定 |
| 14 / 15 | YEAR / DATE | 不把显示宽度当物理宽度 |
| 18 / 19 / 20 | TIMESTAMP / DATETIME / TIME | datetime_precision → fsp |
| 21 | DECIMAL | numeric_precision、numeric_scale |
| 17 | BIT | char_length → bit_length |
| 16 / 29 | VARCHAR / CHAR，或 VARBINARY / BINARY | collation_id、char_length |
| 22 / 23 | ENUM / SET | elements中的有序Base64字典 |
| 24 / 25 / 26 / 27 | TINY / MEDIUM / LONG / 普通 TEXT或BLOB | collation_id和声明容量 |

旧日期编号、旧 DECIMAL、JSON、GEOMETRY 等不自动等同于已支持编码。未知编号报告不支持，不拼出缺列的 schema。

自动文本映射当前只接受已验证的 collation255（utf8mb4_0900_ai_ci）；63表示 binary，用于区分字符串与二进制类型。报告还可识别8为latin1，但这不表示自动读行已支持latin1文本。数字/日期字段的 collation 不能直接套用文本转码规则；也不能假设所有排序规则编号都可以由编码名称推算。

## 长度的单位必须随类型解释

贯穿样本 [lesson_rows](../../testdata/mysql8045/lesson_rows.json) 的 `name` 是 VARCHAR(32)。官方 [SDI预期](../../testdata/sdi/mysql8045_lesson_rows.expected.json.gz) 对应：

```json
{"name":"name","type":16,"char_length":128,"collation_id":255,"is_nullable":true}
```

以上摘录省略了其他键。这里128是最大字节数，utf8mb4每字符最多4字节，因此生成 `max_chars=128/4=32`。它既不是本条记录的实际字符串长度，也不是128个字符；实际长度仍从每条记录的变长长度数组读取。

对应规则：

- utf8mb4 VARCHAR/CHAR：要求最大字节长度可被4整除，转换为 max_chars，再由既有 schema 校验边界。
- binary VARBINARY/BINARY：char_length直接映射max_bytes，不除4。
- TEXT/BLOB：char_length核对类型容量，不把全部容量当作本地记录长度。
- BIT：char_length表示位数；物理字节数由既有BIT解码器计算。
- DECIMAL：使用precision/scale计算物理宽度，不使用显示长度。
- 日期时间：使用类型和fsp计算固定长度，不使用显示长度。

真实 `dates/year_values.ibd.gz` 中 YEAR列的 `is_unsigned=true`，但我们的YEAR schema不接受unsigned参数。这不是丢掉符号信息：YEAR物理字节本来就按既有YEAR规则解释。`metadataColumn` 仅将该属性传递给整数、FLOAT/DOUBLE、DECIMAL。报告 `ColumnMetadata.Unsigned` 保留原DD值，生成schema只保存解码器需要的属性。

## ENUM/SET 字典不是 SQL 文本

[enum_labels的SDI预期](../../testdata/sdi/enums_enum_labels.expected.json.gz) 中，`value` 的前四个成员为：

| index | SDI name（Base64文本） | 解码后的标签 |
|---|---|---|
| 1 | 空串 | 合法空标签 |
| 2 | cmVhZHk= | ready |
| 3 | 5Lit5paH8J+YgA== | 中文😀 |
| 4 | Mg== | 2 |

本列 `options` 为 `interval_count=7;`。实现验证成员index连续、从1开始，Base64与UTF-8有效，数量和interval_count一致，随后交给既有ENUM/SET schema校验。

字典成员中的index不是列数组下标，也不是本条记录的值。真正的ENUM序号/SET掩码仍从用户记录读取；例如合法空标签的ENUM序号1与零号错误值不能合并。不能对DDL字符串做逗号分割，因为标签可以包含需要转义的字符；大型65535成员字典还可能通过SDI_BLOB链取得。

## 逻辑列、引擎隐藏列与索引字段

lesson_rows 的 columns数组顺序为：

```text
column_opx  0      1       2       3          4
            id     score   name    DB_TRX_ID  DB_ROLL_PTR
hidden      1      1       1       2          2
```

columns.hidden 是枚举：1可见，2引擎隐藏，3SQL隐藏，4用户不可见。不能将它当布尔值。聚簇索引elements.hidden则是布尔值，含义不同。

该样本 PRIMARY.elements 的顺序是 `[0,3,4,1,2]`。第一个元素hidden=false，其余hidden=true：后面的score/name也是普通用户列，只是被引擎作为聚簇记录载荷补入索引定义，不能删掉！最终物理顺序为：

```text
id(4) → DB_TRX_ID(6) → DB_ROLL_PTR(7) → score(4或NULL) → name(变长或NULL)
```

用户输出仍按声明顺序 `[id,score,name]`。当前只允许一个显式、升序、非NULL整数主键。实现核对主键元素全宽、系统列类型/长度/顺序，以及剩余字段是否符合现有解码布局。系统列在DD中的整数类型编号也不能直接当用户整数解码：它们有专用物理长度和含义。

## API 和失败边界

[metadata.go](../../metadata.go) 的三个层次是：

1. `InspectTable` 调用 `ReadSDI`，定位单个Table和Tablespace，生成列/索引报告并核对实际根页。
2. `metadataColumn` 映射类型；只有无Issues且原schema校验通过时，报告才有非nil的Schema。
3. `ReadAuto` 使用该Schema调用原Read；错误时不返回部分行。原显式Read仍可使用。

报告列保留DD类型、原始长度/精度/unsigned/nullable、字符集编号及隐藏属性；索引报告见下一章。元数据解释暂限已验证版本80045/80023/80019。不支持特性能够被当前结构解释时保留Issues；缺失关键字段、非法数字、重复键或身份冲突报ErrCorrupt，无法唯一定位Table/Tablespace等报ErrUnsupported，均不返回部分报告。它不是任意MySQL版本的数据字典浏览器。

关键键必须精确存在且非null。零、false、空字典与缺失值有区别；解码器不会让Go零值掩盖缺失元数据，也不会让JSON大小写宽松匹配覆盖规范键。私有属性按转义的 `key=value;` 语法读取，不是SQL表达式。

最后，SDI描述布局，不能证明事务提交状态、只插入历史或快照一致性；自动schema没有解除前面章节的输入限制。
