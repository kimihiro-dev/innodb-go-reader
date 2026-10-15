# InnoDB 物理文件解析手册

按真实样本逐步解释文件、页、记录、列、索引、LOB 与元数据。当前范围见[第 83 章](83-release-support-matrix.md)，复验和故障定位见[第 84 章](84-release-validation.md)；旧章的限制保留教学历史，不能当作现行兼容性。操作示例见[CLI 指南](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md)，全部文档入口见[文档导航](../README.md)。

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
| [83 首版支持矩阵](83-release-support-matrix.md) | 如何判断文件范围并选择正确入口？ | 首版基线与后续扩展，已核对 |
| [84 首版交付与复验](84-release-validation.md) | 如何构建、重跑验收并定位失败？ | 已交付，已验证 |
| [85 统计模型与指标](85-statistics-model-and-metrics.md) | 六视图如何从物理字段得到精确统计？ | 已实现，已验证 |
| [86 统计验证](86-statistics-validation.md) | 如何核对字节、精度、筛选及失败输出？ | 已实现，已验证 |
| [87 MySQL 5.7 显式元数据](87-mysql57-explicit-schema.md) | 如何在没有SDI时采集schema、读取旧BLOB并验证5.7文件？ | 已实现，已验证 |
| [88 MySQL 5.7 二级与钻取](88-mysql57-secondary-indexes.md) | 如何补齐聚簇定位、核对前缀字节并进行覆盖/回表查询和索引钻取？ | 已实现，已验证 |
| [89 MySQL 5.7 系统空间](89-mysql57-system-tablespace.md) | 如何定位旧字典、事务槽、change buffer和双写页？ | 知识文档，源码/样本核对 |
| [90 MySQL 8.0 系统空间](90-mysql80-system-tablespace.md) | mysql.ibd、独立undo/双写/redo如何迁移，哪些旧入口仍保留？ | 知识文档，源码/样本核对 |
| [91 CLI输入准备](91-cli-input-preparation.md) | 如何采集可信定义并生成完整分区清单？ | 已实现，已验证 |
| [92 5.7补丁兼容](92-mysql57-patch-compatibility.md) | 哪些明确补丁已验证，源码/物理布局和TLS连接有什么差异？ | 已实现、已验证 |
| [93 8.0补丁兼容](93-mysql80-patch-compatibility.md) | SDI版本、旧INSTANT字段计数及早期LOB如何兼容？ | 已实现、已验证 |
| [94 分区版本兼容](94-partition-version-compatibility.md) | 5.7可信目录与8.0补丁SDI如何验证完整集合？ | 已实现、已验证 |
| [95 独立页与本地记录](95-local-page-reading.md) | 如何只读必要页并区分原始字节、局部记录、页外引用与完整行？ | 已实现、已验证 |
| [96 字段级物理来源](96-field-provenance.md) | 如何定位载荷、NULL位、长度项及INSTANT默认，并区分DROP、系统与LOB来源？ | 已实现、已验证 |
| [97 聚簇列投影](97-clustered-projection.md) | 如何扫描/查询所需列、按需读取LOB并保留来源、预算及完成边界？ | 已实现、已验证 |
| [98 结构化校验与诊断](98-structured-diagnostics.md) | 如何收集独立问题、定位来源并区分失败、依赖缺失与诊断完成？ | 已实现、已验证 |
| [99 行列存储分析](99-row-column-storage.md) | 如何精确分解列载荷、公共记录开销、INSTANT默认和去重LOB页容量？ | 已实现、已验证 |
| [100 聚簇与二级交叉校验](100-index-consistency.md) | 如何在可信导出前提下发现缺项、多项、值错配并避免误认证？ | 已实现、已验证 |

按主题和前置概念阅读上表章节；样本中的版本和局部假设用于解释物理格式，当前支持及限制统一见第83章。各章的真实字节、源码入口与验证方法直接用于理解现有实现。

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

每章给出相关 Go 入口和测试位置。阅读顺序为结构解释 → 真实字节 → 解码结果 → 验证和边界；源码定位以对应章节为准，不在目录重复维护函数清单。

01–86章格式核查主要使用 MySQL 官方 **mysql-8.0.45** 标签；5.7扩展与系统空间教材另分别核查 **mysql-5.7.44 / mysql-8.0.45**：

- [page0types.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/page0types.h)：页头字段、数据区和系统记录位置。
- [rem0rec.ic](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/rem0rec.ic)：记录头与 next-record 计算。
- [fsp0types.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/fsp0types.h)：表空间格式标志。
- [fil0fil.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/fil0fil.h)：FIL 页头。
- [mach0data.ic](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/mach0data.ic)：整数的物理编码。

这些源文件按章节在实施或教材编写时核对。89–90章是系统空间背景知识，其源码/固定字段核对不代表读取器已实现系统空间解析或恢复。Java 1.0.10 用于辅助理解，其行为不代替官方版本的格式定义。
