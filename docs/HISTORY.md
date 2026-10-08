# 历史验收记录

**性质：按阶段归并的历史证据索引。**

本文件集中保存原需求、决议、任务列表和阶段计划中的验收正文；同一组验收只保存一份合并记录，保留各来源独有的数字、异常和边界。数量、测试次数、环境和支持边界均指验收当时，不代表当前新增验证或任意组合兼容。当前范围见[需求](REQUIREMENTS.md)和[支持矩阵](format/83-release-support-matrix.md)；完整规划见[阶段计划](STAGE_PLANS.md)，设计取舍见[历史决议](history/DECISIONS.md)。

## 阶段索引

| 阶段 | 内容 | 解析手册 |
|---|---|---|
| [1](#stage-1) | 从简单 InnoDB 表空间完整还原行数据 | [01](format/01-table-to-file.md) / [02](format/02-file-to-page.md) / [03](format/03-page-to-record.md) / [04](format/04-record-to-values.md) / [05](format/05-verification.md) |
| [2](#stage-2) | 多页、多层聚簇索引整表扫描 | [06](format/06-index-tree.md) / [07](format/07-node-pointers.md) / [08](format/08-tree-scan.md) |
| [3](#stage-3) | 整数类型解析 | [09](format/09-integer-values.md) / [10](format/10-integer-tree-validation.md) |
| [4](#stage-4) | 页内变长字段 | [11](format/11-variable-lengths.md) / [12](format/12-variable-validation.md) |
| [5](#stage-5) | 非压缩 LOB 页外数据 | [13](format/13-lob-reference-and-pages.md) / [14](format/14-lob-validation.md) |
| [6](#stage-6) | TEXT/BLOB 与多页 LOB 索引 | [15](format/15-text-blob-types.md) / [16](format/16-lob-index-pages.md) |
| [7](#stage-7) | DECIMAL 精确小数 | [17](format/17-decimal-encoding.md) / [18](format/18-decimal-validation.md) |
| [8](#stage-8) | FLOAT / DOUBLE 浮点数 | [19](format/19-floating-point-encoding.md) / [20](format/20-floating-point-validation.md) |
| [9](#stage-9) | DATE / YEAR 日期与年份 | [21](format/21-date-year-encoding.md) / [22](format/22-date-year-validation.md) |
| [10](#stage-10) | DATETIME 与小数秒 | [23](format/23-datetime-encoding.md) / [24](format/24-datetime-validation.md) |
| [11](#stage-11) | TIME 与负时长 | [25](format/25-time-encoding.md) / [26](format/26-time-validation.md) |
| [12](#stage-12) | TIMESTAMP 与 UTC | [27](format/27-timestamp-encoding.md) / [28](format/28-timestamp-validation.md) |
| [13](#stage-13) | BIT 位字段 | [29](format/29-bit-encoding.md) / [30](format/30-bit-validation.md) |
| [14](#stage-14) | BINARY 定长二进制 | [31](format/31-binary-encoding.md) / [32](format/32-binary-validation.md) |
| [15](#stage-15) | ENUM 枚举与原始序号 | [33](format/33-enum-encoding.md) / [34](format/34-enum-validation.md) |
| [16](#stage-16) | SET 集合与位掩码 | [35](format/35-set-encoding.md) / [36](format/36-set-validation.md) |
| [17](#stage-17) | utf8mb4 CHAR 与存储空格 | [37](format/37-char-encoding.md) / [38](format/38-char-validation.md) |
| [18](#stage-18) | 读取路径中的页校验 | [39](format/39-page-checksum.md) / [40](format/40-checksum-validation.md) |
| [19](#stage-19) | SDI 原始元数据提取 | [41](format/41-sdi-records.md) / [42](format/42-sdi-validation.md) |
| [20](#stage-20) | 自动生成 schema 与可靠定位索引根 | [43](format/43-sdi-schema.md) / [44](format/44-metadata-roots-validation.md) |
| [21](#stage-21) | JSON 完整存储值解析 | [45](format/45-binary-json.md) / [46](format/46-json-opaque-validation.md) |
| [22](#stage-22) | 空间类型存储值解析 | [47](format/47-geometry-storage.md) / [48](format/48-geometry-validation.md) |
| [23](#stage-23) | 字符集与字符列边界扩展 | [49](format/49-charset-storage.md) / [50](format/50-charset-validation.md) |
| [24](#stage-24) | 复合整数聚簇键 | [51](format/51-composite-key-layout.md) / [52](format/52-composite-key-validation.md) |
| [25](#stage-25) | 字符串、二进制及其他聚簇键 | [53](format/53-typed-key-layout.md) / [54](format/54-key-order-validation.md) |
| [26](#stage-26) | 无显式主键的表 | [55](format/55-clustered-identity.md) / [56](format/56-rowid-layout-validation.md) |
| [27](#stage-27) | UPDATE/DELETE 后的当前物理记录 | [57](format/57-delete-mark-layout.md) / [58](format/58-change-snapshot-validation.md) |
| [28](#stage-28) | 更新后的 LOB 与完整当前值 | [59](format/59-updated-lob-layout.md) / [60](format/60-lob-update-validation.md) |
| [29](#stage-29) | INSTANT ADD/DROP COLUMN 与行布局版本 | [61](format/61-instant-row-layout.md) / [62](format/62-instant-validation.md) |
| [30](#stage-30) | 生成列与不可见列 | [63](format/63-generated-column-layout.md) / [64](format/64-generated-validation.md) |
| [31](#stage-31) | COMPACT 与旧式页外链 | [65](format/65-compact-row-layout.md) / [66](format/66-compact-lob-validation.md) |
| [32](#stage-32) | 流式读取与资源限制 | [67](format/67-streaming-contract.md) / [68](format/68-streaming-lob-and-budgets.md) |
| [33](#stage-33) | 主键点查与范围扫描 | [69](format/69-key-query-navigation.md) / [70](format/70-query-validation.md) |
| [34](#stage-34) | 二级索引物理记录解析 | [71](format/71-secondary-record-layout.md) / [72](format/72-secondary-validation.md) |
| [35](#stage-35) | 二级索引查询、回表与投影 | [73](format/73-secondary-query-navigation.md) / [74](format/74-projection-and-lookup-validation.md) |
| [36](#stage-36) | 表空间分配结构与页分析 | [75](format/75-space-allocation-layout.md) / [76](format/76-space-analysis-validation.md) |
| [37](#stage-37) | 正式命令行与可复用导出 | [77](format/77-cli-and-lossless-export.md) / [78](format/78-cli-validation-and-failure.md) |
| [38](#stage-38) | 分区表文件集合读取 | [79](format/79-partition-collection-layout.md) / [80](format/80-partition-validation-and-cli.md) |
| [39](#stage-39) | 页结构可视化与学习浏览器 | [81](format/81-visual-report-model.md) / [82](format/82-visual-report-validation.md) |
| [40](#stage-40) | 支持矩阵冻结与首版交付 | [83](format/83-release-support-matrix.md) / [84](format/84-release-validation.md) |

<a id="stage-1"></a>

## 阶段 1：从简单 InnoDB 表空间完整还原行数据

**日期：2026-09-09。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-1)。

验证范围：第一阶段 Go 实现已完成，七组真实表共 57 行全部对照通过；go test/race/vet 与 10 秒 fuzz 通过，七个夹具通过 innochecksum。只新增专用测试库，未修改既有用户表；未运行 Java 测试。详细证据见[第 05 章](format/05-verification.md)。

<a id="stage-2"></a>

## 阶段 2：多页、多层聚簇索引整表扫描

**日期：2026-09-09。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-2)。

第二阶段验证：10 组共 14557 行逐列对照通过；真实深树 1715 叶子 + 2 中间 + 1 根页。test/race/vet、10 秒 FuzzTree（98046 次）通过，核心包覆盖率 92.8%；新增三个原始文件通过 innochecksum。原夹具不变，新增专用库 innodb_reader_fixture_7e30cb376715。

<a id="stage-3"></a>

## 阶段 3：整数类型解析

**日期：2026-09-09。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-3)。

第三阶段验证：新增十组 2526 行，全项目二十组 17083 行逐列 SQL 对照通过。test/race/vet 通过，核心包覆盖率 95.2%；10 秒 FuzzIntegerTree 完成 60091 次执行，无失败。十个新增文件通过 innochecksum；示例程序 501 行与 SQL 独立对照，保留 uint64 最大值；手册字段字节与本地链接核对通过。最终专用测试库 innodb_reader_fixture_33e1abae3280；初次验证库 innodb_reader_fixture_304006962e18 保留，未改动既有用户表。 覆盖全部整数主键宽度/符号、64 位高位节点和极值；手册 09–10 交付。

<a id="stage-4"></a>

## 阶段 4：页内变长字段

**日期：2026-09-10。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-4)。

第四阶段验证：新增五组成功夹具共 365 行、两组真实 external 拒绝夹具（4 行，不计作已还原）。全项目 25 组成功夹具、17448 行 SQL 对照通过，另有两组拒绝夹具。test/race/vet 通过，核心包覆盖率 96.2%；10 秒 FuzzVariableTree 完成 23298 次执行，无失败。七个新增文件通过 innochecksum；示例 10 行经 Base64 解码逐字节对照 SQL HEX；手册关键字节/偏移已独立核对。专用测试库 innodb_reader_fixture_00d36aeb9abc，未改动既有表。 手册 11–12 交付；合法记录复用碎片及活动字节核算通过。

<a id="stage-5"></a>

## 阶段 5：非压缩 LOB 页外数据

**日期：2026-09-10。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-5)。

第五阶段验证：三个新夹具共 92 行，原两组页外拒绝样本 4 行转为成功（原始资产不变）。全项目 30 组 17544 行 SQL 对照通过；新增 153 个页外字段、309 个数据块，覆盖单块/多块容量边界、多列多行与 UTF-8 字符跨页。test/race/vet 通过，核心包覆盖率 96.6%；10 秒 FuzzLOB 完成 19905 次执行，无失败。三个新增文件通过 innochecksum；示例 8 行 Base64 解码后与 SQL HEX 完全一致，手册引用/索引字节已核对。专用测试库 innodb_reader_fixture_f340b8050b59，未改动既有表。 单块 9000 字节与多块 60000 字节实例通过；手册 13–14 交付。该阶段限初始 LOB_FIRST/LOB_DATA 和首个 LOB 页内索引，TEXT/BLOB 与外部索引页随后在阶段 6 扩展。

<a id="stage-6"></a>

## 阶段 6：TEXT/BLOB 与多页 LOB 索引

**日期：2026-09-10。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-6)。

第六阶段验证：六组新夹具，五组 16 行 SQL 对照成功、一组超出 16 MiB 一字节明确拒绝。累计 35 组成功夹具 17560 行，共 36 组资产。覆盖八种类型、外部索引页 10/11/282/283 项边界和 16 MiB 值；test/race/vet 通过，核心包覆盖率 96.8%；10 秒 FuzzLOBIndex 完成 7728 次执行，无失败。六个文件通过 innochecksum，手册 15–16 实际字节与链接已核对。专用测试库 innodb_reader_fixture_0e5bc55c533d；未修改既有用户表。

<a id="stage-7"></a>

## 阶段 7：DECIMAL 精确小数

**日期：2026-09-10。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-7)。

第七阶段验证：19 组新夹具 704 行成功还原，覆盖全部 1580 种 precision/scale、UNSIGNED、混合页外 TEXT、NULL 与跨页树。累计 54 组成功资产共 18264 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.0%；10 秒 FuzzDecimal 完成 175177 次执行，无失败；19 个新文件通过 innochecksum。三个示例表共 608 行与 SQL 独立预期精确对照，手册 17–18 字节/偏移和本地链接核对通过。新增专用库 innodb_reader_fixture_7044725c1e7c，未修改既有用户表。 跨页树含 600 行，SQL 预期按字符串精确比较；可信显式 schema 不保证发现所有错误 precision/scale 配置。

<a id="stage-8"></a>

## 阶段 8：FLOAT / DOUBLE 浮点数

**日期：2026-09-11。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-8)。

第八阶段验证：四组新夹具 890 行 SQL 位模式对照成功，涵盖 FLOAT/DOUBLE、UNSIGNED、正负零、次正规数/极值、精度边界、混合 DECIMAL/LOB 和 600 行树。累计 58 组成功资产 19154 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.1%；10 秒 FuzzFloat 完成 142200 次执行，无失败。四个新文件通过 innochecksum；示例全部 890 行按目标浮点位宽对照通过，负零数字标记保留；手册 19–20 字节/偏移与本地链接已核对。新增库 innodb_reader_fixture_21d5d52d4e45，未修改既有用户表。 SQL/文件/JSON 三层位模式一致；只还原存储后的值，不恢复插入前舍入丢失的精度。

<a id="stage-9"></a>

## 阶段 9：DATE / YEAR 日期与年份

**日期：2026-09-11。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-9)。

第九阶段验证：四组新夹具 1939 行与 SQL 精确对照，覆盖 DATE/YEAR、全部 YEAR 字节、1664 组日期分量、零/部分零/非日历日期、混合 LOB 与跨页树。累计 62 组成功资产 21093 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.3%；10 秒 FuzzDate 完成 180503 次执行，无失败。四个新文件通过 innochecksum；示例全部 1939 行与 SQL 一致，手册 21–22 字节/偏移及本地链接核对通过。新增专用库 innodb_reader_fixture_c26bba055b94，仅生成会话设置 ALLOW_INVALID_DATES，未改全局模式或既有用户表。 YEAR 覆盖全部 256 个字节；DATE 物理分量还原范围不等于官方 SQL 保证范围。

<a id="stage-10"></a>

## 阶段 10：DATETIME 与小数秒

**日期：2026-09-11。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-10)。

第十阶段验证：九组新夹具 716 行 SQL 精确对照，覆盖 DATETIME 全七种 fsp（0..6）、NULL/零/非日历日期、小数极值/尾零、跨日舍入、混合 LOB 与 600 行树。累计 71 组成功资产 21809 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.4%；10 秒 FuzzDatetime 完成 170514 次执行，无失败。九个新文件通过 innochecksum；示例全部 716 行对照成功，手册 23–24 真实字节/偏移和本地链接已核对。新增库 innodb_reader_fixture_625d4a88697d，仅生成会话使用 ALLOW_INVALID_DATES，未改全局模式或既有用户表。

<a id="stage-11"></a>

## 阶段 11：TIME 与负时长

**日期：2026-09-11。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-11)。

第十一阶段验证：九组新夹具 821 行 SQL 精确对照，覆盖 TIME 全精度、负小数借位、正负亚秒/端点/进位、混合 LOB 与 600 行树。累计 80 组成功资产 22630 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.6%；10 秒 FuzzTime 完成 186673 次执行，无失败。九个新文件通过 innochecksum；示例全部 821 行与 SQL 一致；手册 25–26 字节/偏移及本地链接通过核对。新增库 innodb_reader_fixture_e26673c3973a，仅生成会话设置 STRICT_TRANS_TABLES，未改全局模式或既有用户表。

<a id="stage-12"></a>

## 阶段 12：TIMESTAMP 与 UTC

**日期：2026-09-11。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-12)。

第十二阶段验证：十组新夹具 691 行 SQL 精确对照成功，覆盖 TIMESTAMP 全精度、零值/NULL、2038 小数端点、三时区插入与显示、DATETIME 对照、混合 LOB 和 600 行树。累计 90 组成功资产 23321 行，另有一组超限拒绝样本。test/race/vet 通过，核心包覆盖率 97.7%；10 秒 FuzzTimestamp 完成 169604 次执行，无失败。十个文件通过 innochecksum；示例全部 691 行在三个机器时区下与 UTC SQL 预期一致。手册 27–28 字节/偏移及链接核对通过。新增专用库 innodb_reader_fixture_0a36f4d2b0f5，仅改变生成会话 sql_mode/time_zone，未改全局配置或既有用户表。 覆盖最小正常秒与全部七种 fsp；输出固定 UTC，不恢复原会话时区或自动列语义。

<a id="stage-13"></a>

## 阶段 13：BIT 位字段

**日期：2026-09-14。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-13)。

第十三阶段验证：三组新夹具 673 行及示例输出 SQL 精确对照通过，覆盖 BIT 全位宽/逐位值、NULL 与最大 uint64、混合 LOB 和树。累计 93 组成功资产 23994 行，另有一组超限拒绝资产。test/race/vet 通过，核心覆盖率 97.7%；FuzzBit 独立重跑 10 秒 176738 次执行通过（首次结束时报 deadline，无失败输入报告）。三个文件通过 innochecksum，手册 29–30 字节及链接核对完成。原实例未运行，本轮使用临时 MySQL 8.0.45，最终库 innodb_reader_fixture_43ee7f373b02；验证后关闭临时服务，未改原实例。 覆盖全部 64 种位宽及 NULL/零的区分。

<a id="stage-14"></a>

## 阶段 14：BINARY 定长二进制

**日期：2026-09-14。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-14)。

第十四阶段验证：18 组 BINARY 真实夹具 700 行及示例 Base64/HEX 精确对照通过；全部 255 种长度、补零/空格/任意字节、NULL、混合 VARBINARY/LOB 和 600 行树。累计 111 组成功资产 24694 行，另有一组超限拒绝资产。test/race/vet 通过，核心覆盖率 97.7%；FuzzFixedBinary 10 秒预算完成 81248 次执行通过。18 个文件通过 innochecksum，手册 31–32 字节/位图与链接核对完成。临时 MySQL 最终库 innodb_reader_fixture_3b57ac397814，完成后关闭服务，未改原实例。

<a id="stage-15"></a>

## 阶段 15：ENUM 枚举与原始序号

**日期：2026-09-14。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-15)。

第十五阶段验证：七组新夹具 1140 行标签/序号双重 SQL 对照及示例标签输出通过；1/255/256/65535 项字典、NULL/零号/合法空标签、特殊文本和混合 LOB/树。累计 118 组成功资产 25834 行，另有一组超限拒绝资产。test/race/vet 通过，核心覆盖率 97.8%；FuzzEnum 10 秒完成 174081 次执行。七个文件通过 innochecksum，手册 33–34 字节/偏移及链接核对完成。使用原实例新独立库 innodb_reader_fixture_0d4169df4215，仅生成会话模式清空，全局模式未改，实例保持运行。

<a id="stage-16"></a>

## 阶段 16：SET 集合与位掩码

**日期：2026-09-14。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-16)。

第十六阶段验证：四组新夹具 685 行字符串/位掩码 SQL 双重对照、混合 ENUM 序号及示例输出通过；全部 64 种成员数、逐位与最高位、空标签、混合 LOB/树。累计 122 组成功资产 26519 行，另有一组超限拒绝资产。test/race/vet 通过，核心覆盖率 97.9%；FuzzSet 10 秒完成 165906 次执行。四个文件通过 innochecksum，手册 35–36 字节/偏移与链接核对完成。原实例新独立库 innodb_reader_fixture_37ef7488e9e0，仅生成会话设置 STRICT_TRANS_TABLES，全局配置未改，服务保持运行。

<a id="stage-17"></a>

## 阶段 17：utf8mb4 CHAR 与存储空格

**日期：2026-09-14。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-17)。

第十七阶段验证：35 组新夹具 926 行 SQL/CharStorage 对照及示例输出通过，覆盖全 255 种长度、特殊空白、NULL、混合 LOB/树和五个实际页外 CHAR。累计 157 组成功资产 27445 行，另有一组超限拒绝资产。test/race/vet 通过，核心覆盖率 97.9%；FuzzChar 10 秒预算完成 383481 次执行。35 个文件通过 innochecksum，手册 37–38 真实字节/偏移、本地链接、阶段锚点及生成器语法核对通过。原实例新独立库 innodb_reader_fixture_eee9ec340d1e，仅生成会话设置 STRICT_TRANS_TABLES，全局模式未改，服务保持运行。 包含混合 VARCHAR/TEXT；Values 去尾 U+0020，CharStorage 保留实际物理文本。

<a id="stage-18"></a>

## 阶段 18：读取路径中的页校验

**日期：2026-09-15。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-18)。

第十八阶段验证：读取时严格CRC32C及头尾LSN检查完成；157组成功资产27445行回归通过，158文件官方严格crc32校验通过，原始夹具未改。结构测试重封装校验和后继续覆盖原检查。race/vet通过，核心覆盖率98.0%；FuzzChecksum 602586次、FuzzRead 459349次（各10秒预算）通过。手册39–40完成，无实例操作。 头部/正文/尾部损坏和访问路径覆盖测试通过；不认证事务一致性。

<a id="stage-19"></a>

## 阶段 19：SDI 原始元数据提取

**日期：2026-09-15。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-19)。

第十九阶段验证：158原始资产316个SDI对象与ibd2sdi及CLI完全对照，1个真实页外对象跨29页；合成多层/搬根/空根/前缀测试通过。原用户行回归与race/vet通过，核心覆盖率97.2%；FuzzSDI10秒预算258953次通过。手册41–42完成，无MySQL实例操作；本地官方源码8.0.45作为后续参考基线。 手册真实字节/偏移、本地引用及预期提取脚本语法通过；258953 次为最终代码重新执行的单次结果，不累计早先运行。

<a id="stage-20"></a>

## 阶段 20：自动生成 schema 与可靠定位索引根

**日期：2026-09-15。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-20)。

第二十阶段离线验证：158资产自动/手工schema及完整Read结果一致；CLI逐字节对照27445成功行、1超限拒绝。test/race/vet通过，核心包覆盖率93.1%；FuzzMetadata 10秒预算11674次通过。合成搬根、二级入口诊断、缺失/损坏元数据和公开ReadAuto无部分结果测试通过。手册43–44真实字节、52文档本地文件链接及生成器语法/帮助检查通过。

第二十阶段真实验收：用户要求重试后连接审批恢复，新独立库innodb_reader_metadata_2b94e27b44eb采集五快照，保存到testdata/metadata。五文件官方严格crc32、10个SDI对象官方/CLI对照、SQL索引身份和列定义对照通过；三个可读快照共12行成功，两个二级索引快照明确拒绝且无部分结果。重建后二级root复用5，index ID404→405；删除后SDI正确移除索引。TestMetadataLifecycle已纳入默认离线回归，完整race/vet通过，核心覆盖率93.1%。累计163资产，其中160成功资产27457行，另有1超限、2二级索引拒绝；按快照统计，不代表不同逻辑行数。手册新增真实页复用/遗留页头字节说明。原实例保持运行，未改既有用户表或全局配置；本阶段无剩余待办，

<a id="stage-21"></a>

## 阶段 21：JSON 完整存储值解析

**日期：2026-09-16。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-21)。

第二十一阶段验证：5组真实夹具636行、5页外字段、3大容器、2 DECIMAL/3时间/3二进制opaque节点与SQL精确语义/类型/深度/长度对照通过，自动和显式schema结果一致。5文件官方严格crc32、10 SDI对象及类型树/普通视图CLI对照通过。race/vet通过，核心覆盖率92.8%；FuzzJSON 10秒849857次无失败；损坏、100层/100000节点/16MiB边界与无部分返回通过。累计168资产、165成功资产28093行及3拒绝资产。原实例新独立库innodb_reader_json_2bf7dabe3618，仅设置生成会话sql_mode/time_zone；原实例保持运行，既有表与全局配置未改。手册45–46与资产来源说明已交付， 合成 TIMESTAMP/内部 decimal 验证单独标明，不冒充真实 SQL 样本。

<a id="stage-22"></a>

## 阶段 22：空间类型存储值解析

**日期：2026-09-16。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-22)。

第二十二阶段验证：12组真实夹具628行、1页外值、600行七页树、3行SRID4326轴序与SQL WKB/SRID/类型/坐标对照通过；自动和手工schema及完整结果一致。12文件官方严格CRC32、24个官方SDI对象及CLI对照通过。race/vet通过，核心覆盖率92.9%；FuzzGeometry 10秒预算2584478次无失败。累计180资产、177成功资产28721行及3拒绝资产。手册47–48完成；新独立库innodb_reader_geometry_c7eae7464cb2，未改既有表或全局配置，原实例保持运行。

<a id="stage-23"></a>

## 阶段 23：字符集与字符列边界扩展

**日期：2026-09-16。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-23)。

第二十三阶段验证：21组真实快照1064行、四字符集/九collation、全部128个ASCII及256个latin1字节、90次字典数值对照、四类零宽字段、8个页外字段及600行20页树通过SQL文本/HEX/完整CHAR视图验证；自动/手工schema和完整结果一致。21文件官方严格CRC32、42个官方SDI对象及CLI通过。完整race/vet通过，核心覆盖率93.4%；FuzzCharset 10秒预算621192次无失败。累计201资产、198成功资产29785行及3拒绝资产。手册49–50完成。新库innodb_reader_charset_c3a4f995f69a，未改既有表或全局配置，原实例保持运行。

<a id="stage-24"></a>

## 阶段 24：复合整数聚簇键

**日期：2026-09-17。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-24)。

第二十四阶段验证：6组真实快照3614行、混合宽度/符号、64位端点、非连续声明位置、共享前缀、页外字段及16列主键3000行278页三层树与SQL全键ORDER BY完全一致，自动/手工schema及完整结果一致。6文件官方严格CRC32、12个官方SDI对象及CLI通过。完整race/vet通过，核心覆盖率93.6%；10秒预算FuzzCompositeOrder 2580325次、FuzzCompositeRead 537次无失败。重复/后续成员倒序/完整元组范围/截断/SDI拒绝和无部分结果通过。累计207资产、204成功资产33399行及3既有拒绝资产。手册51–52完成，新库innodb_reader_composite_25c6d3d89012；未改既有表或全局配置，原实例保持运行。

<a id="stage-25"></a>

## 阶段 25：字符串、二进制及其他聚簇键

**日期：2026-09-17。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-25)。

第二十五阶段验证：144份新增真实快照24433行、70个类型矩阵多页树及3000行494页三层树，四个_bin规则/ASC-DESC/原编码PAD SPACE/控制字节/latin1欧元/完整3072字节键/普通LOB均通过SQL全键排序、自动与手工schema及完整结果对照。官方144文件严格CRC32、288个SDI对象、CLI全部通过。完整race/vet通过，核心覆盖率93.7%；10秒预算FuzzKeyOrder 58722次、FuzzKeyRead 5次通过（大文件种子，执行数较少）。累计351资产、348成功资产57832行及3既有拒绝资产。手册53–54完成。最终库innodb_reader_keys_bedf314bb4a3，未改既有表或全局配置，原实例保持运行。 损坏及无部分返回测试通过；具体矩阵和边界见对应手册。

<a id="stage-26"></a>

## 阶段 26：无显式主键的表

**日期：2026-09-17。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-26)。

第二十六阶段验证：10份新增真实快照12621行，覆盖无索引/空表/可空与前缀唯一索引/重复用户行/LOB/多个唯一索引/字符复合DESC及显式主键加二级索引；ROW_ID三层树12000行1719聚簇页，字符唯一树600行48页。SQL有序或多重集合、全索引SQL身份、自动/手工schema、官方10文件CRC/20个SDI对象及CLI全部通过。旧二级拒绝资产2份转为聚簇读取成功（8行），原资产不改。完整race/vet通过，核心覆盖率93.9%；FuzzRowID 10秒673916次、FuzzHiddenPage 10秒19642次通过，补充unsigned拒绝后定向race通过。累计361资产、360成功资产70461行及1既有超限拒绝。手册55–56完成，最终新库innodb_reader_cluster_4fbb5a8ad9f7；只调整生成会话的主键相关变量，未改全局或既有用户表，原实例保持运行。

<a id="stage-27"></a>

## 阶段 27：UPDATE/DELETE 后的当前物理记录

**日期：2026-09-17。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-27)。

第二十七阶段验证：16份真实变更快照4081行当前值、4016条delete-mark摘要，覆盖增长/缩短/NULL/空串、主键更新、删除重插、隐藏ROW_ID及字符复合DESC；真实树从4000行816页level2收缩到10行单根叶页。删除键集合独立对照、Raw/链计数、SQL有序或多重集合、索引身份、自动/手工schema、官方16文件CRC/32个SDI对象及CLI全部通过。完整race/vet通过，核心覆盖率93.9%；FuzzChangedPage 10秒预算8557次无失败。原361资产不改，累计377资产、376成功资产74542行及1既有超限拒绝。手册57–58完成，最终库innodb_reader_changes_a6dcd49fc617；两会话仅操作新库/会话变量，写事务提交后采集，原实例/全局配置保持。

<a id="stage-28"></a>

## 阶段 28：更新后的 LOB 与完整当前值

**日期：2026-09-18。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-28)。

第二十八阶段验证：17份真实快照54行，TEXT/BLOB继承/全量替换/增长缩短/NULL空值/删除、JSON小修改不增版本/大修改换块/多轮历史/空洞及重用，最长13项历史链、单值286历史项和两个额外索引页、purge后保留分配页，均通过SQL精确语义/HEX、自动与手工schema、官方17文件CRC/34个SDI对象及CLI对照。完整race/vet通过，核心覆盖率94.2%；10秒预算FuzzUpdatedLOB 31980次、FuzzJSON 700008次无失败。原377资产不改，累计394资产、393成功资产74596行及1既有超限拒绝。手册59–60完成，最终库innodb_reader_lob_updates_5d9c5681a7cc；未改全局或既有用户表，原实例保持运行。

<a id="stage-29"></a>

## 阶段 29：INSTANT ADD/DROP COLUMN 与行布局版本

**日期：2026-09-18。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-29)。

第二十九阶段验证：17份真实DDL快照3916行，覆盖非末尾ADD、多列/多轮DROP、同名重增、NULL/非NULL与精确类型默认、当前默认变更不追溯、隐藏ROW_ID、1001行85页非叶子树、实际版本64及FORCE/INPLACE重建清零；SQL精确值/多重集合、自动与显式schema、官方17文件CRC/34个SDI对象和CLI全部通过。最终全量race/vet通过，核心覆盖率94.1%；10秒预算FuzzInstantRecord 362933次无失败。原394资产不改，累计411资产、410成功资产78512行及1既有超限拒绝。手册61–62完成，最终库innodb_reader_instant_e85d0f225972；仅操作新库及会话设置，未改全局或既有用户表，实例保持运行。

<a id="stage-30"></a>

## 阶段 30：生成列与不可见列

**日期：2026-09-18。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-30)。

第三十阶段验证：14份真实快照，13份共1232行物化值成功、1份函数索引内部隐藏列明确拒绝；覆盖STORED/INVISIBLE/VIRTUAL混合、SQL NULL、基础列更新、不可见主键、STORED唯一聚簇键、隐藏ROW_ID重复行、两个页外字段、102页树及INSTANT增删/重建。SQL指定列/多重集合、自动与显式结果、14文件官方严格CRC32、28个SDI对象及CLI通过。全量race/coverage与vet通过，核心覆盖率94.3%；FuzzMaterializedRead十秒预算86663次无失败，新增分类/兼容检查定向race通过。原411资产未改，累计425资产、423份在对应完整或仅物化入口成功79744行及2份拒绝资产。手册63–64完成。最终独立临时库innodb_reader_generated_d64ef4eedc8a，临时实例已关闭，原实例和既有用户表未修改。

<a id="stage-31"></a>

## 阶段 31：COMPACT 与旧式页外链

**日期：2026-09-20。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-31)。

第31阶段验证：44份新增真实快照7732行，普通与官方debug writer各22份3866行；COMPACT/DYNAMIC配对覆盖768前缀、UTF-8跨界、新旧LOB同一行、旧链1/2/3页边界、长值增长缩短/NULL/删除、JSON、隐藏ROW_ID、767字节DESC键、102页树及INSTANT/STORED/VIRTUAL组合。SQL精确值/多重集合、自动与显式schema、44文件官方严格CRC32、88个SDI对象及CLI全部通过。全量race/coverage和vet通过，核心覆盖率94.2%；FuzzCompactLOB十秒312537次无失败。累计469资产、467成功87476行及2份既有拒绝。手册65–66完成。普通库innodb_reader_compact_01d906c9c5fa、debug库innodb_reader_compact_867fd3538ef5位于独立临时实例，均已正常关闭，原实例及既有表未改。旧链样本明确限同版本官方debug写入路径，不宣称历史版本兼容。

<a id="stage-32"></a>

## 阶段 32：流式读取与资源限制

**日期：2026-09-20。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-32)。

第32阶段验证：用户确认的逐行完整值回调＋独立原始LOB块流已实现，新增Scan/ScanAuto/ScanMaterialized/ScanMaterializedAuto、StreamLOB及Complete/计数协议、context取消、有界LRU缓存与资源预算。共享树和新旧LOB校验，旧Read原子契约与16MiB类型物化上限保持。469份现有资产对照通过，467份成功87476行、359个页外值与完整结果/来源一致，2份类型/布局拒绝保持；既有16777217字节资产独立流1028块与SQL长度/SHA一致。9份CLI快照12620行、预算和完整/错误报告通过。全量race/coverage、缓存优化后的流式定向race及vet通过，核心覆盖率94.0%；FuzzStreaming十秒968106次、FuzzStreamLOB十秒586023次无失败。12000行真实表Read保留结果GC后存活堆约59MB，Scan在1000/12000行的采样峰值约1.2MB（排除输入及调用方保留数据，非RSS硬上限）；3轮基准Read约37.2ms/133459381 B-op，Scan约34.8ms/121940218 B-op。手册67–68、示例、可复跑验证脚本及基准已交付。原始快照未改，无MySQL实例操作。

<a id="stage-33"></a>

## 阶段 33：主键点查与范围扫描

**日期：2026-09-20。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-33)。

第33阶段验证：四个Query入口、严格键编码、完整键点查/开闭与无界范围/完整前导列Prefix/Reverse/Limit、目录定位及子树剪枝已实现，共享流式取消/缓存/预算与部分输出协议。3份新快照2606行、92条独立SQL查询3939行与自动/显式schema一致，官方CRC/SDI和CLI通过。累计472资产，470份成功90082行及2份既有拒绝；1386次点查、686次范围/limit、482次前缀查询通过。12000行三层树点查3个聚簇页（整树1718页），25行范围正反向均6页；访问/未访问路径损坏、未命中LOB、停止/取消/预算/输入所有权测试通过。全量race/coverage及vet通过，核心覆盖率94.4%；FuzzQueryRanges十秒55923次、FuzzQueryKeys十秒1943449次无失败。手册69–70、查询CLI及可复跑生成/官方验证脚本完成。新库innodb_reader_query_801d543ea403位于独立临时实例，现已正常关闭；原实例/既有表与旧快照未改。 上下界按索引声明顺序、Reverse 独立反转输出，空范围与 Limit 为成功结束；Complete 仅认证请求范围及访问路径。

<a id="stage-34"></a>

## 阶段 34：二级索引物理记录解析

**日期：2026-09-21。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-34)。

第34阶段验证：用户确认的独立SecondarySchema/SecondaryRecord/SecondaryNode模型、InspectSecondary、ReadSecondary/ReadSecondaryAuto、ScanSecondary/ScanSecondaryAuto已实现。支持普通二级物理字段、完整聚簇定位映射、NULL/重复键/唯一非NULL约束、混合ASC/DESC、CHAR/VARCHAR/BINARY/VARBINARY前缀与delete-mark，共享页结构校验和扫描预算。12份新快照5249当前表行、20棵二级树8150当前项及60删除项、216二级页与独立SQL/受控DML对照通过；2000行字符复合索引有182页、三层树。官方12文件CRC/24个SDI对象及CLI、原有资产/损坏/取消/预算/所有权测试通过。全量race/coverage及vet通过，核心覆盖率93.8%；补充删除键集合定向race通过，FuzzSecondaryPage十秒395387次无失败。累计484资产、482份成功95331行及2份既有拒绝。手册71–72和可复跑采集/官方验证脚本已交付。新库innodb_reader_secondary_12402f8b6069位于隔离临时实例，现已正常关闭，原实例/既有用户表和历史快照未改。

<a id="stage-35"></a>

## 阶段 35：二级索引查询、回表与投影

**日期：2026-09-21。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-35)。

第35阶段功能验收：四个QuerySecondary入口、完整原列谓词、保守前缀导航、覆盖/按需回表投影、独立结果来源、延迟LOB及共享累计预算已实现。新增3快照3482当前行，358条独立SQL查询27340行，既有12快照20二级树342次查询通过；官方3文件CRC/6个SDI对象与CLI一致。深树点查3二级页对比整树182页；完整副本提前排除119/120候选，仅回表1次。NULL/空串、混合方向、隐藏ROW_ID、INSTANT/STORED/VIRTUAL显式入口、缺失/矛盾定位、未访问损坏、取消/预算/所有权与LOB选择通过。累计487资产，484份完整/仅物化成功98811行，3份完整读取拒绝。十秒预算FuzzSecondaryQueryRange474802次、FuzzSecondaryPrefixRanges8713次无失败，后者种子race通过；vet通过。手册73–74、示例及生成/验证脚本完成。新库innodb_reader_secondary_query_bdd66c509cc1位于隔离临时实例，已正常关闭，旧资产及既有用户表未改。

第35阶段最终回归：`go test -race -cover -timeout=25m ./...`通过（822.508秒），核心包覆盖率93.7%；`go vet ./...`通过。三份快照SHA、保存查询计数、手册LOB字节、Python语法及本地文档链接复核通过。此前默认10分钟超时由延长测试超时解决，产品预算未改。

<a id="stage-36"></a>

## 阶段 36：表空间分配结构与页分析

**日期：2026-09-23。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-36)。

第36阶段验收完成（2026-09-23）：用户确认的严格AnalyzeSpace、独立SpaceOptions/SpaceReport、FSP/XDES/INODE/IBUF_BITMAP、分配链/位图/段/索引归属及局部页结构验证已实现，错误返回nil报告，合法空闲残留与未知使用中页有明确契约。487份既有资产空间回归与4份新专用快照通过；新快照34842页、8个SDI对象经官方CRC/页统计/SDI、独立位图和SQL身份对照验证。17408页样本跨16384页描述区的第二组XDES/IBUF_BITMAP并覆盖状态5租用区；删除后文件不缩小、活动页17080→18及重建身份变化均有真实证据。全量race/coverage通过（857.205秒，核心包92.6%），最终身份边界补充后全部空间专项race通过（15.278秒），vet通过；最终FuzzSpaceAllocation十秒495751次无失败（不与早先次数累计）。手册75–76、示例及生成/验证脚本完成，源字节/脚本语法/文档链接核对通过。原487份读行资产未改，原484份完整/仅物化成功98811行和3份拒绝基线保持；另4份.space.gz只作空间验收，共491份空间快照。新库innodb_reader_space_2b493d901bb5使用用户提供的本地实例，采集后未停止实例，未改既有用户表或全局设置。

<a id="stage-37"></a>

## 阶段 37：正式命令行与可复用导出

**日期：2026-09-28。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-37)。

第37阶段验收完成（2026-09-28）：正式metadata/page/export/query/check/space命令、公开rowio编码/读回、版本1带类型JSONL/CSV、可选物理来源、明确验证范围、取消/预算/退出码及单文件原子发布已交付，无新增依赖。484份既有成功资产的98811行分别通过两种格式逐值/Go类型/列定义/来源往返，3份完整读取拒绝基线保持。聚簇范围与二级投影、VIRTUAL严格/显式物化、原报告对照、真实子进程SIGINT/断管、截断/空白CSV/短写/损坏末页、目标竞态及输入/查询文件身份保护通过。全量race/coverage通过（核心885.541秒、92.6%）；最后边界修正后的全部新模块race再次通过（rowio148.179秒、89.7%，CLI最终9.018秒、90.0%）。vet通过；最终FuzzDecoder十秒预算1223385次无失败，不累计早先运行次数。手册77–78、README与协议约定已同步，211处本地文档链接及lesson真实字节/SHA核对通过。未修改原始快照内容，未操作MySQL实例；

第37阶段验收补充：CSV读回显式拒绝空白物理行，避免encoding/csv将其跳过/当作EOF而遗漏后续数据；JSONL/CSV逐记录边界及预算单独验证。所有输出路径把短写转换为io.ErrShortWrite，帮助输出错误也保留；main忽略默认SIGPIPE并将实际管道关闭归为退出1。physical对象移除Values键，原字段以RawMessage保持精度。新增测试均以临时副本或独立输出目录运行，不修改历史夹具。

<a id="stage-38"></a>

## 阶段 38：分区表文件集合读取

**日期：2026-09-29。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-38)。

第38阶段验收完成（2026-09-29）：严格完整集合InspectPartitions、ScanPartitions及显式Materialized入口、逐分区定义序与来源、跨文件累计预算及缓存隔离、CLI --manifest元数据/导出/check rows和原子输出已交付。34份独立分区快照组成12组集合，11组成功3184行，1组子分区明确拒绝；覆盖RANGE/LIST/HASH/KEY、空分区、多页/LOB、COMPACT、隐藏ROW_ID、VIRTUAL及首/非首分区重建和EXCHANGE。34文件官方CRC、46个官方SDI对象、SQL分区定义/表身份/逐分区有序值/全表多重集合及两种CLI导出通过；索引物理身份与官方ibd2sdi对照，未误称空INNODB_INDEXES结果为SQL索引验证。 采集后测试实例保持运行。

缺失/重复/额外/跨表/旧新混配、SDI目录/index_opx/列所有者/逻辑物理属性冲突、后段损坏、精确预算/差一预算、取消/停止/回调所有权、清单尺寸/输入保护/部分输出及原子文件通过。全量race/coverage通过（核心854.269秒、92.5%；CLI8.602秒、89.1%；rowio150.904秒、89.7%）。最后逻辑索引属性边界补充后，全部分区专项race再次通过（核心4.298秒、CLI2.251秒）；vet通过，FuzzPartitionDirectory十秒预算7119次无失败，随后最后边界种子race通过。原487份单表资产、484成功98811行及3拒绝基线和4份空间专用资产保持。手册79–80、README、约定、固定夹具及可复跑脚本完成；手册命令、真实字节、34份SHA/大小、Python语法和本地链接复核通过。

<a id="stage-39"></a>

## 阶段 39：页结构可视化与学习浏览器

**日期：2026-09-30。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-39)。

第39阶段验收完成（2026-09-30）：默认严格空间层，显式单索引完整树及所选页详情，精确整数、自包含HTML、累计源读取与报告预算、取消及原子输出已实现，无新增依赖。6类固定样本的数据/字节/脚本语法验证通过，包括182页二级树与17408页空间；三份交付HTML与最终CLI输出逐字节一致。用户确认页号定位、父子页、记录高亮和LOB块导航正常，记录为用户本地浏览器验收，不声称自动截图或跨浏览器测试。 visual.Build/WriteHTML 与 visualize、手册 81–82、三份离线示例和可复跑脚本交付；失败/取消/原子输出测试通过。

中断后的完整回归重新执行并通过：`go test -race -cover -timeout=25m ./...`（核心857.153秒、92.5%；rowio149.512秒、89.7%；CLI9.786秒、89.2%；visual缓存通过、91.4%）。最终visual专项race实际运行10.373秒通过；vet通过，最终FuzzHTMLReport十秒预算32808次无失败。原487份单表资产、484成功98811行及3拒绝、4份空间资产和34份分区资产基线保持；未修改原始快照或操作MySQL实例。手册字节/本地链接及脚本语法复核完成，

<a id="stage-40"></a>

## 阶段 40：支持矩阵冻结与首版交付

**日期：2026-09-30。状态：已完成（历史验收）。** [原计划](STAGE_PLANS.md#stage-40)。

第40阶段验收完成（2026-09-30）：首版范围冻结为既有MySQL8.0.45/16KiB/DYNAMIC与COMPACT能力，交付手册83–84、支持/拒绝/未验证矩阵、API/失败/预算索引、组合与官方/SQL证据索引、故障复现及scripts/verify_release.py。README的整数键、旧LOB、显式schema与流式失败过期表述已修正。首版按日期标识本地基线，没有扩大解析支持或创建远程发布。

复验记录保存在[report.json](release-validation/report.json)及同目录日志，complete=true。Go1.25.7 darwin/arm64，完整race/coverage通过（核心92.5%、rowio89.7%、visual91.4%命中有效缓存；CLI实际9.003秒、89.2%），vet通过；四项各十秒预算主动fuzz：Read347914次、PartitionDirectory7244次、Decoder605370次、HTMLReport18160次无失败。其余fuzz种子由完整测试覆盖。沿用同日第39阶段核心857.153秒的已完成完整回归证据，不把缓存结果称为重新跑完长回归。

3次本机扫描基准：Read35.981ms/133457557 B-op，Scan33.679ms/121940224 B-op；12000行Read保留结果GC后存活堆58963552字节，Scan1000/12000行采样峰值1154320/1211176字节。计时不含解压/磁盘I/O，存活堆非RSS或硬保证。CLI成功/拒绝/失败保留目标、复验脚本失败报告及现有目录保护、预算故障复现、文档链接与Python语法检查通过。

源码与所有testdata文件指纹前后一致；487单表资产484成功98811行/3拒绝、4空间资产、34分区资产11成功集合3184行/1拒绝组保持。原SQL/官方工具证据明确作为历史独立证据引用，无新增MySQL操作或原始快照修改。第1–40阶段主线全部完成；可选扩展保持未实施，本次收尾无剩余待办。

## 主线完成后的交付记录

<a id="task-52"></a>

### 任务 52：Java 参考引用迁移

**日期：2026-09-30。状态：已完成（历史验收）。**

迁移验证完成：GitHub 1.0.10标签与固定提交已确认，文件树未截断；22个引用文件的Git blob SHA与原调研快照一致，58处文件/目录链接及行号检查通过。仅修改参考分析、需求、决策和任务状态文档，无运行代码变更。 参考目录保持只读，未删除。

<a id="task-53"></a>

### 任务 53：CLI 使用指南

**日期：2026-10-08。状态：已完成（历史验收）。**

交付 [命令行使用指南](CLI.md)，覆盖七个命令、全部公开参数、查询/分区清单、无损导出及失败/预算说明；README与手册目录增加入口。19组shell示例实跑34次CLI请求（33成功、1预期预算失败），七命令帮助参数与38处指南链接核对通过；17个导出文件共2040行经rowio读回，精确查询值、覆盖/回表来源、SQL前缀查询预期、3分区1000行及8类失败核对通过。仅更新文档，源码与原始夹具指纹保持。 同时核对 end 完成状态、3 分区顺序及失败时原目标保持；修改 5 份既有文档并新增 CLI.md，未重复首版长回归。

<a id="task-54"></a>

### 任务 54：Git 初始化与推送

**日期：2026-10-08。状态：已完成（历史验收）。**

初次尝试：CLI构建与 `go test ./internal/cli`（实际3.380秒）通过。初始提交 `7edb6dd` 包含3069文件、约20.03MiB，其中2809个testdata文件和110个Go文件；已重新核对提交内容、忽略规则及干净工作区。正常推送实际因GitHub HTTPS凭据缺失失败；钥匙串无可用github.com凭据，SSH agent无已加载密钥，未写入远端。该次只完成本地提交，远端推送随后重试。

后续重试：经 GitHub 官方 443 端口 SSH 入口，正常建立 kimihiro-dev/innodb-go-reader 的 main并设置origin/main跟踪关系，首轮远端main与本地HEAD均为 `f43797a2337e3f22e4efd06a7cce6e0431eaf43d`。源码、固定夹具和已有提交完整保留；仅新增仓库本地SSH配置与准确的项目状态记录。完成状态文档随收尾提交正常推送，并再次核对最终HEAD/远端一致和干净工作区；没有新增解析变更，无需重复先前已通过的构建/CLI测试。

<a id="task-55"></a>

### 任务 55：v0.1.0 发布准备

**日期：2026-10-08。状态：已完成（历史验收）。**

交付[六平台构建指引](RELEASING.md)和[发布说明](releases/v0.1.0.md)，由用户手动创建 GitHub Release。 验收完成：Go1.25.7在macOS ARM64执行文档中的完整Bash命令，六平台编译通过；Mach-O/ELF/PE格式和GOOS/GOARCH/CGO_ENABLED/-trimpath信息正确，四个tar.gz与两个zip各仅包含对应程序，Unix执行权限及全部SHA256通过。本机--help/metadata/check/export四项烟测通过，lesson_rows四行精确值和JSONL end一致。v0.1.0附注标签已正常推送，远端标签对象及解引用提交均与本地一致，固定116de5046aa94b379ce4d2e0711e34be9766c397；文档在main单独提交。未改Go/夹具，未创建GitHub Release，未重复既有长回归。

<a id="task-56"></a>

### 任务 56：文档整理与维护规则

**日期：2026-10-08。状态：已完成（整理验收）。**

新增文档导航，明确当前状态、技术手册、操作指南与历史材料的正文职责；按阶段合并相似验收记录，保留独有数字、异常与边界。76 条施工期决议按原顺序归档，现行选择使用稳定 D 编号；任务 1–56、40 个阶段及路线图锚点完整。维护规则由 [D010](DECISIONS.md#d010文档分工与唯一正文)约束后续修改。

统一 84 章标题、章内小节、状态/日期与 Markdown 间距；逐章对照整理前快照，代码围栏、数据表及全部旧标题锚点保持。2977 个原有非文档/原始验证文件指纹一致，其中 19 份首版原始报告/日志保持；Go 实现、固定夹具和 v0.1.0 源码标签未改。

[只读文档检查器](../scripts/check_docs.py)通过 97 份 Markdown、889 处本地引用/锚点及源码行号检查；临时样本验证 1 个合法输入和 9 类预期失败，包括断链、缺失锚点、未关闭代码围栏、标题跳级、重复锚点、断开的表格、缺失末尾换行、越界源码行号及混合小节编号。Python 语法与 `git diff --check` 通过；本轮为文档整理，未重复历史 Go 长回归或操作 MySQL。

<a id="task-57"></a>

### 任务 57：文档整理的 Git 交付

**日期：2026-10-08。状态：进行中。**

用户授权提交并推送本次整理；沿用 [D008](DECISIONS.md#d008git-初始化与发布保持用户授权边界)的正常推送和 [D009](DECISIONS.md#d009v010固定既有源码文档独立演进)的标签约束。验收为本次文档与检查器提交到 main，远端提交与本地一致，工作区干净，v0.1.0 解引用保持固定源码提交。
