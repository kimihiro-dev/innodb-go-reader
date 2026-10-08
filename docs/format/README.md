# InnoDB 物理文件解析手册

这份手册和 Go 实现一起演进。已完成单页行还原、多页多层聚簇索引扫描，以及全部整数类型、页内变长字段、TEXT/BLOB、跨页初始与更新后非压缩 LOB 、DECIMAL 精确小数、FLOAT/DOUBLE 、DATE/YEAR 、DATETIME 小数秒、TIME 负时长、TIMESTAMP UTC 、BIT 位字段、BINARY 定长二进制、ENUM 字典、SET 位掩码及 utf8mb4 CHAR 存储空格解析，并在读取路径验证 CRC32C/头尾 LSN，支持独立提取 SDI 原始元数据、生成受支持表的schema并自动读行，也可解析JSON列的容器、精确数值和opaque类型树，以及空间列的SRID/WKB与二维几何结构；字符列支持utf8mb4、utf8mb3、ascii、MySQL latin1和零宽字段，聚簇主键支持1..16列多类型元组和每成员ASC/DESC，字符键限四个已验收_bin规则；支持无显式主键时的唯一非空聚簇键和隐藏DB_ROW_ID，允许普通二级索引共存，并通过独立接口解析受支持的二级物理记录。支持受控写事务已结束快照中的页内UPDATE/DELETE、更新后LOB当前值和JSON部分更新空洞，并分开返回当前物理行和删除摘要。支持原生INSTANT ADD/DROP的混合行版本、物理列映射和历史默认补全。支持STORED/INVISIBLE物化列及显式仅物化读取入口，VIRTUAL单独报告、不伪装SQL NULL。你不需要先读 Java 参考代码，也不必先理解整个 InnoDB 存储引擎。

当前首版范围统一见[第83章](83-release-support-matrix.md)，构建、复验、独立证据与故障定位见[第84章](84-release-validation.md)。旧章节的阶段性限制保留作学习上下文。

操作工具请读[命令行使用指南](../CLI.md)：集中说明七个正式命令、参数、查询与分区清单、导出格式，并提供固定样本使用示例。

## 阅读顺序

| 章节 | 读完能回答的问题 | 状态 |
|---|---|---|
| [01 从表到文件](01-table-to-file.md) | 哪个文件存着行？怎样拿到可靠样本？ | 已实现、已验证 |
| [02 从文件到数据页](02-file-to-page.md) | 怎样定位页，知道它属于哪个索引？ | 已实现、已验证 |
| [03 从数据页到记录](03-page-to-record.md) | 怎样找出记录，为什么指针会往回跳？ | 已实现、已验证 |
| [04 从记录到列值](04-record-to-values.md) | NULL、负数、中文在文件里是什么样？ | 已实现、已验证 |
| [05 验证与复现](05-verification.md) | 如何复跑、重新造数据、验证边界？ | 已实现、已验证 |
| [06 多页索引树](06-index-tree.md) | 表变大后行和页面如何组织？ | 已实现、已验证 |
| [07 非叶子记录](07-node-pointers.md) | 键、MIN_REC 和子页号怎样解码？ | 已实现、已验证 |
| [08 整树扫描](08-tree-scan.md) | 怎样不丢行、不重复地读取整表？ | 已实现、已验证 |
| [09 整数列值](09-integer-values.md) | 整数宽度、符号、NULL 和精度如何影响解码？ | 已实现、已验证 |
| [10 整数主键与验证](10-integer-tree-validation.md) | 如何用完整 64 位整数导航并验证整树？ | 已实现、已验证 |
| [11 页内变长字段](11-variable-lengths.md) | 如何读取一/两字节长度并恢复文本、二进制？ | 已实现、已验证 |
| [12 变长字段验证](12-variable-validation.md) | 如何验证碎片、页外边界与二进制输出？ | 已实现、已验证 |
| [13 页外引用与 LOB 页](13-lob-reference-and-pages.md) | 如何从 20 字节引用找到完整字段？ | 已实现、已验证 |
| [14 页外数据验证](14-lob-validation.md) | 如何验证容量边界、字符跨页和损坏引用？ | 已实现、已验证 |
| [15 TEXT/BLOB 类型](15-text-blob-types.md) | 类型字节容量、TINY 长度编码和输出如何对应？ | 已实现、已验证 |
| [16 多页 LOB 索引](16-lob-index-pages.md) | 如何区分分配链与活动链，完整恢复大值？ | 已实现、已验证 |
| [17 DECIMAL 编码](17-decimal-encoding.md) | 如何从定长字节精确还原正负小数？ | 已实现、已验证 |
| [18 DECIMAL 验证](18-decimal-validation.md) | 混合记录如何定位，怎样覆盖全部精度组合？ | 已实现、已验证 |
| [19 浮点编码](19-floating-point-encoding.md) | 如何还原小端浮点数、负零和精度边界？ | 已实现、已验证 |
| [20 浮点验证](20-floating-point-validation.md) | SQL、原始位与 JSON 如何精确对照？ | 已实现、已验证 |
| [21 日期与年份编码](21-date-year-encoding.md) | 三字节日期与一字节年份如何还原？ | 已实现、已验证 |
| [22 日期与年份验证](22-date-year-validation.md) | 如何区分零值、NULL 与非日历日期并验证混合记录？ | 已实现、已验证 |
| [23 DATETIME 编码](23-datetime-encoding.md) | 如何拆解五字节主体和小数秒？ | 已实现、已验证 |
| [24 DATETIME 验证](24-datetime-validation.md) | 如何验证七种精度、跨日舍入与混合记录？ | 已实现、已验证 |
| [25 TIME 编码](25-time-encoding.md) | 负时长与小数秒借位怎样恢复？ | 已实现、已验证 |
| [26 TIME 验证](26-time-validation.md) | 如何验证正负端点、舍入与混合记录？ | 已实现、已验证 |
| [27 TIMESTAMP 编码](27-timestamp-encoding.md) | 四字节秒数、小数和零值如何还原为 UTC？ | 已实现、已验证 |
| [28 TIMESTAMP 验证](28-timestamp-validation.md) | 时区、混合记录与 SQL 显示如何对照？ | 已实现、已验证 |
| [29 BIT 编码](29-bit-encoding.md) | 位宽、字节序和未使用高位如何解释？ | 已实现、已验证 |
| [30 BIT 验证](30-bit-validation.md) | 全位宽、NULL/LOB 和整树如何验证？ | 已实现、已验证 |
| [31 BINARY 编码](31-binary-encoding.md) | 固定二进制、补零和字段偏移如何解释？ | 已实现、已验证 |
| [32 BINARY 验证](32-binary-validation.md) | 如何覆盖全部长度并区分空值与 NULL？ | 已实现、已验证 |
| [33 ENUM 编码](33-enum-encoding.md) | 如何从物理序号恢复标签并保留零号含义？ | 已实现、已验证 |
| [34 ENUM 验证](34-enum-validation.md) | 标签/序号如何独立对照并验证字典边界？ | 已实现、已验证 |
| [35 SET 编码](35-set-encoding.md) | 字典成员、位掩码和空标签如何对应？ | 已实现、已验证 |
| [36 SET 验证](36-set-validation.md) | 全成员数、混合 ENUM/LOB 与 SQL 如何对照？ | 已实现、已验证 |
| [37 CHAR 编码](37-char-encoding.md) | 字符数、物理字节与两层空格处理如何区分？ | 已实现、已验证 |
| [38 CHAR 验证](38-char-validation.md) | 全声明长度、页外 CHAR 与 SQL 如何对照？ | 已实现、已验证 |
| [39 页校验](39-page-checksum.md) | 两个 CRC32C 区间与头尾 LSN 怎样检查？ | 已实现、已验证 |
| [40 校验验证](40-checksum-validation.md) | 怎样区分校验损坏测试与结构测试？ | 已实现、已验证 |
| [41 SDI记录](41-sdi-records.md) | 如何无需用户schema找到并解压元数据？ | 已实现、已验证 |
| [42 SDI验证](42-sdi-validation.md) | 如何读取页外链并与官方工具对照？ | 已实现、已验证 |
| [43 SDI到schema](43-sdi-schema.md) | 如何区分DD类型、字节长度与逻辑/物理列？ | 已实现、已验证 |
| [44 索引根验证](44-metadata-roots-validation.md) | 如何核对元数据与实际索引入口？ | 已实现、真实索引变更验收通过 |
| [45 二进制JSON](45-binary-json.md) | 容器目录、inline值、偏移和类型树如何对应？ | 已实现、已验证 |
| [46 JSON扩展与验证](46-json-opaque-validation.md) | 如何保留decimal/时间/opaque，并读取页外JSON？ | 已实现、已验证 |
| [47 空间存储值](47-geometry-storage.md) | SRID、WKB、坐标与集合层次怎样还原？ | 已实现、已验证 |
| [48 空间值验证](48-geometry-validation.md) | 如何核对轴序、页外几何、SQL与损坏边界？ | 已实现、已验证 |
| [49 字符集与布局](49-charset-storage.md) | 如何区分编码、原始字节和CHAR定长/变长布局？ | 已实现、已验证 |
| [50 字符集验证](50-charset-validation.md) | 零宽字段、跨LOB字符及SQL文本/HEX如何对照？ | 已实现、已验证 |
| [51 复合键记录布局](51-composite-key-layout.md) | 主键顺序与SQL列顺序怎样映射，系统字段放在哪里？ | 已实现、已验证 |
| [52 复合树验证](52-composite-key-validation.md) | 完整元组、MIN_REC与跨层范围怎样比较和验证？ | 已实现、已验证 |
| [53 多类型键物理布局](53-typed-key-layout.md) | 变长主键如何改变长度数组、事务字段和子页位置？ | 已实现、已验证 |
| [54 键排序与验收](54-key-order-validation.md) | PAD SPACE、原编码和DESC如何决定整个树的范围？ | 已实现、已验证 |
| [55 无显式主键的聚簇身份](55-clustered-identity.md) | 怎样识别实际唯一聚簇索引及隐藏PRIMARY/GEN_CLUST_INDEX？ | 已实现、已验证 |
| [56 ROW_ID布局与验收](56-rowid-layout-validation.md) | 六字节系统键如何区分重复用户行并导航多层树？ | 已实现、已验证 |
| [57 delete-mark与记录变化](57-delete-mark-layout.md) | 删除、迁移、purge和空间复用如何改变记录链？ | 已实现、已验证 |
| [58 变化快照与树收缩](58-change-snapshot-validation.md) | 如何独立验收当前行、删除摘要和真实三层树合并？ | 已实现、已验证 |
| [59 更新后的LOB](59-updated-lob-layout.md) | 引用、块版本、历史链与purge如何影响当前值读取？ | 已实现、已验证 |
| [60 JSON空洞与更新验收](60-lob-update-validation.md) | 部分更新后如何分辨有效载荷、残留字节与历史块？ | 已实现、已验证 |
| [61 INSTANT行布局](61-instant-row-layout.md) | 当前列序、物理位置、ADD默认与DROP字段怎样关联？ | 已实现、已验证 |
| [62 INSTANT验证与重建](62-instant-validation.md) | 非叶子NULL位图、版本64和重建清零怎样验收？ | 已实现、已验证 |
| [63 生成列与物理字段](63-generated-column-layout.md) | STORED、VIRTUAL、INVISIBLE怎样映射到实际字节？ | 已实现、已验证 |
| [64 仅物化接口与验证](64-generated-validation.md) | 如何区分未物化与NULL，验证混合布局和完整行契约？ | 已实现、已验证 |
| [65 COMPACT 行布局](65-compact-row-layout.md) | 前缀、引用和实际 LOB 页类型如何分别判断？ | 已实现、已验证 |
| [66 COMPACT 与旧链验证](66-compact-lob-validation.md) | 如何走旧 BLOB 链并复核配对 SQL 和损坏边界？ | 已实现、已验证 |
| [67 流式扫描协议](67-streaming-contract.md) | 已输出前缀、取消和最终完成状态如何处理？ | 已实现、已验证 |
| [68 LOB块流与预算](68-streaming-lob-and-budgets.md) | 如何读取超大原始字段并测量内存？ | 已实现、已验证 |
| [69 聚簇键查询导航](69-key-query-navigation.md) | 如何按索引顺序定位范围并跳过无关子树？ | 已实现、已验证 |
| [70 查询编码与验收](70-query-validation.md) | 如何保持精确键值并用独立SQL验证边界？ | 已实现、已验证 |
| [71 二级记录布局](71-secondary-record-layout.md) | 前缀、NULL和聚簇定位字段如何存储？ | 已实现、已验证 |
| [72 二级整树验收](72-secondary-validation.md) | 如何验证独立二级模型、SQL及删除标记？ | 已实现、已验证 |
| [73 二级查询导航](73-secondary-query-navigation.md) | 完整原列谓词如何使用前缀索引、覆盖与回表？ | 已实现、已验证 |
| [74 投影与回表验收](74-projection-and-lookup-validation.md) | 如何按需读取LOB并共享预算、核对SQL？ | 已实现、已验证 |
| [75 表空间分配布局](75-space-allocation-layout.md) | 页、区、段、位图和分配链如何对应？ | 已实现、已验证 |
| [76 空间分析验收](76-space-analysis-validation.md) | 如何区分空闲残留、分配占用和页内指标？ | 已实现、已验证 |
| [77 正式命令与无损导出](77-cli-and-lossless-export.md) | 如何完整保留NULL、类型和数值精度？ | 已实现、已验证 |
| [78 命令失败与发布](78-cli-validation-and-failure.md) | 如何判断流式成功并避免失败覆盖文件？ | 已实现、已验证 |
| [79 分区集合布局](79-partition-collection-layout.md) | 如何把逻辑定义映射到多个物理文件？ | 已实现、已验证 |
| [80 分区命令与验证](80-partition-validation-and-cli.md) | 如何验证完整性、来源和重建后身份？ | 已实现、已验证 |
| [81 离线可视化模型](81-visual-report-model.md) | 空间、树与字节如何保持可追溯？ | 已实现，已验证 |
| [82 报告命令与验证](82-visual-report-validation.md) | 如何生成报告并验证导航和失败范围？ | 已实现，已验证 |
| [83 首版支持矩阵](83-release-support-matrix.md) | 如何判断文件范围并选择正确入口？ | 已冻结，已核对 |
| [84 首版交付与复验](84-release-validation.md) | 如何构建、重跑验收并定位失败？ | 已交付，已验证 |

01–05 章保留第一阶段样本及当时的支持边界；第二阶段扩展以 06–08 章为准，第三阶段整数扩展以 09–10 章为准，第四阶段页内变长字段以 11–12 章为准，第五阶段初始非压缩 LOB 以 13–14 章为准，第六阶段 TEXT/BLOB 与多页索引以 15–16 章为准，第七阶段精确小数以 17–18 章为准，第八阶段浮点以 19–20 章为准，第九阶段日期与年份以 21–22 章为准，第十阶段 DATETIME 以 23–24 章为准，第十一阶段 TIME 以 25–26 章为准，第十二阶段 TIMESTAMP 以 27–28 章为准，第十三阶段 BIT 以 29–30 章为准，第十四阶段 BINARY 以 31–32 章为准，第十五阶段 ENUM 以 33–34 章为准，第十六阶段 SET 以 35–36 章为准，第十七阶段 CHAR 以 37–38 章为准，第十八阶段读取时校验以 39–40 章为准，第十九阶段SDI提取以41–42章为准，第二十阶段自动schema以43–44章为准，第二十一阶段JSON以45–46章为准。后续阶段以本目录及阶段计划的最新状态为准，二级查询见73–74章，当前不能读取任意 MySQL 表。

## 贯穿案例与记号

主要样本为 `testdata/mysql8045/lesson_rows.ibd`。除明确标为算法示意外，本阶段偏移、十六进制和输出均来自交付的这个文件。其 SHA256 为：

```text
16ce56bac7c881db15f075dc97d67ede6ce09a763c65449b068f0b79fa8fa678
```

- 数字偏移默认十进制；`0x` 开头为十六进制。
- **文件偏移**从整个 `.ibd` 的第一个字节算起。
- **页内偏移**从当前页的第一个字节算起。
- **记录 origin**是记录数据部分起点，不包括前面的记录头和变长元信息。
- `[a,b)` 表示从 a 到 b 前一个字节；长度为 b−a。
- `NULL` 表示 SQL NULL；空字符串是长度为 0 的非 NULL 值。

重建夹具后，space/index ID、LSN、事务字段和文件哈希可能改变，不要将本章的样本数字当作格式常量。页头和记录头结构应以对应 MySQL 版本源码核对。

## 源码与阅读方式

Go 入口：`schema.go` 定义可信元数据与边界，`sdi.go` 提取原始元数据，`metadata.go` 映射schema并核对索引根，`materialized.go` 显式读取物化列并返回未物化说明，`charset.go` 处理字符集与文本布局，`geometry.go` 解码空间值与坐标树，`json.go` 解码二进制JSON并生成类型树与普通视图，`checksum.go` 验证校验和，`page.go` 读取页，`integer.go` 解码整数，`decimal.go` 解码精确小数，`float.go` 解码浮点数，`date.go` 解码日期与年份，`datetime.go` 解码 DATETIME 与小数秒，`time.go` 解码 TIME 时长，`timestamp.go` 解码 UTC 时间点，`bit.go` 解码位字段，`enum.go` 解码枚举标签与序号，`set.go` 解码集合与位掩码，`variable.go` 解码变长元数据，`lob.go` 还原页外值，`record.go` 还原页内记录，`key.go` 比较主键元组，`tree.go` 遍历整树并解码导航记录。每章先解释结构，再指出相应函数；测试见 `reader_test.go`、`tree_test.go`、`integer_test.go`、`variable_test.go`、`lob_test.go`、`large_lob_test.go`、`decimal_test.go`、`float_test.go`、`date_test.go`、`datetime_test.go`、`time_test.go`、`timestamp_test.go`、`bit_test.go`、`fixed_binary_test.go`、`enum_test.go`、`set_test.go`、`char_test.go`、`checksum_test.go`、`sdi_test.go`、`metadata_test.go`、`json_test.go`、`geometry_test.go`、`charset_test.go`、`composite_test.go`。原 parseLeaf 已扩展为 parseIndex，原页内 Read 逻辑移至 decodePage。

格式核查使用 MySQL 官方 **mysql-8.0.45** 标签：

- [page0types.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/page0types.h)：页头字段、数据区和系统记录位置。
- [rem0rec.ic](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/rem0rec.ic)：记录头与 next-record 计算。
- [fsp0types.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/fsp0types.h)：表空间格式标志。
- [fil0fil.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/fil0fil.h)：FIL 页头。
- [mach0data.ic](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/mach0data.ic)：整数的物理编码。

这些源文件已在实现时下载核对。Java 1.0.10 用于辅助理解，其行为不代替 MySQL 8.0.45 的格式定义。
