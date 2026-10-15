# 83. 首版支持矩阵与入口选择

## 基线与判断方法

本仓库交付根包及rowio/stats；下文CLI入口说明对应独立应用，应用命令、采集器和HTML实现不再包含于本模块。

当前源码以当前分支最新提交（`HEAD`）为准。本章保留2026-09-30首版样本，并集中记录源码已验收扩展；历史v0.1.0不包含后续能力。[第84章](84-release-validation.md)给出首版构建、验收与故障复现，后续证据见[验证资料](../README.md#验证资料)。旧章节保留各阶段的教学上下文，其中“当前不支持”可能已由后续阶段扩展，当前承诺以本章、实际API和最新需求为准。

先确定文件版本与快照来源，再检查格式、元数据和请求入口。`metadata`成功只表示元数据报告已生成；必须检查Issues及Schema/MaterializedSchema，不能据此认定完整行可读。`check --scope rows`、`space`、局部查询各自认证的范围不同。

“支持”指下述限制内已有实现和证据，并不表示类型、DDL、索引与存储布局的任意笛卡尔积都经过实测。“明确拒绝”指已覆盖的特征或损坏模式有拒绝路径；未知版本可能没有可靠标记，不能承诺所有越界输入都能自动识别。

## 文件、快照与记录布局

| 维度 | 首版范围 | 边界与证据 |
|---|---|---|
| 版本 | MySQL 8.0.45 | 不推断8.0其他补丁、8.4、9.x或MariaDB兼容；旧BLOB来自同版本官方debug写入路径，不代表旧版本兼容 |
| 物理文件 | 16 KiB、独立非压缩非加密表空间 | 其他页大小、压缩/透明压缩、加密、系统/通用表空间不在范围；可检测布局拒绝 |
| 行格式 | DYNAMIC、COMPACT | REDUNDANT、COMPRESSED未实现；[65–66章](65-compact-row-layout.md) |
| 校验 | 当前crc32格式的严格CRC32C、头尾LSN低32位 | 不自动降级到旧/禁用校验；空闲残留的空间分类另按分配证据处理 |
| 输入时刻 | 目标写事务已结束的受控稳定快照 | 不对正在变化的在线文件提供一致性保证；无redo/undo回放、MVCC可见性判断或事务恢复 |
| DML | 已验收的UPDATE/DELETE、delete-mark、purge和树收缩 | 当前未删除物理行与删除摘要分离；不恢复删除历史值；[57–60章](57-delete-mark-layout.md) |
| DDL | 8.0.45原生INSTANT ADD/DROP、混合行版本、默认补全及重建 | 旧式INSTANT、升级混合布局未支持；[61–62章](61-instant-row-layout.md) |
| 生成列 | STORED和INVISIBLE物化列；VIRTUAL显式仅物化视图 | 完整入口拒绝无法求值的VIRTUAL；仅物化入口不插入伪NULL；函数索引内部隐藏列拒绝 |
| 分区 | 完整普通RANGE/LIST/HASH/KEY独立分区集合 | 只提供元数据、逐分区定义序扫描与导出；不做子分区、分区路由、集合查询或全局归并；[79–80章](79-partition-collection-layout.md) |

## MySQL 8.0明确补丁扩展

2026-10-14当前源码在既有8.0.45之外新增8.0.11、8.0.12、8.0.16、8.0.19、8.0.23、8.0.28、8.0.30的单表自动SDI读取。采用准确MySQL/DD/SDI组合，不放开未测试中间补丁。历史v0.1.0不含此扩展。

新增证据覆盖16 KiB非压缩非加密DYNAMIC/COMPACT、代表列类型、普通/隐藏聚簇及多层树、普通DESC二级/字符前缀和覆盖/回表、当前LOB与生成列；8.0.12/16/19/23/28旧式INSTANT追加、8.0.23/28/30不可见列、8.0.30现代ADD/DROP及DYNAMIC GIPK。原始SDI单独保留已删除对象，自动发现排除它们。详细版本组合、字节、适配与精度/组合边界见[第93章](93-mysql80-patch-compatibility.md)，验收见[8.0补丁报告](../mysql80-patch-validation/report.json)。

2026-10-14分区集合扩展见[第94章](94-partition-version-compatibility.md)；升级后的混合版本文件仍未验收；函数/多值索引、系统/通用空间、压缩/加密和恢复范围不变。下文完整8.0.45矩阵不能直接外推所有新补丁组合。

当前排除8.0.29的兼容与验收，官方撤回依据及版本边界见[第93章](93-mysql80-patch-compatibility.md#验证与范围)。

## 表级动态元数据版本修正

2026-10-09 后续源码修正表级 `version` 的误拒绝，按可选 uint64 动态元数据处理；解释、输入边界与验证方式见[第 43 章](43-sdi-schema.md#表级动态元数据版本2026-10-09-修正)。MySQL、SDI、行格式及 INSTANT 支持范围沿用上表。历史标签 `v0.1.0` 不包含此修正，使用时需要按最新提交重新编译。

## 独立页与本地记录入口

当前库提供`ReadRawPage/ReadPage/ReadClusteredPage/ReadSecondaryPage`；模型、真实字节及验证方法见[第95章](95-local-page-reading.md)。原始页不作内容校验；校验页只验证严格CRC32C、头尾LSN低32位和请求页号，未知类型只返回通用页头及字节，不认证表空间格式或分配。

聚簇/二级局部入口通过可信Schema验证页0及目标INDEX页，不读取SDI、其他根、兄弟、子页或LOB。聚簇`LocalRecord.Values`以`ExternalField`表示未解引用值，`nil`仅为SQL NULL，VIRTUAL不求值；删除标记仅返回证据，非叶页返回节点。二级返回独立物理项，局部检查有效非NULL唯一键。子页号仅检查非零、非自身和文件范围，不认证目标子页。

这些接口失败均返回nil；原始/校验页读取一次，索引局部入口最多读取页0与目标页两次，同步调用无独立context/预算选项。局部成功不认证当前分配、父子范围、兄弟互链、跨页唯一性、整树、完整列值、LOB或事务可见性；正确Schema和稳定输入仍由调用方保证，格式/版本范围不变。正式CLI的page命令仍按全空间分析执行。

## 字段级物理来源入口

`TraceClusteredPage/TraceSecondaryPage`复用上述局部读取并显式收集Sources，不改变普通Read/Scan/Query的值或逐字段分配。记录origin关联字段的实际物理序、当前逻辑列、页内载荷区间、长度项和NULL位，区分stored/default/dropped/system；VIRTUAL只在聚簇追踪的独立定义集合报告。INSTANT默认编码来自可信Schema，无伪造本地位置；DROP残留只解释布局，不解码历史值。

外部Data包含本地Prefix与20字节Reference子区间，追踪仍只读页0及目标页，Chunks由后续显式StreamLOB或完整读取提供。追踪失败返回nil；外部标记载荷不足20字节时包括删除/DROP字段也拒绝，以免给出越界来源。坐标、真实字节、所有权及验证方法见[第96章](96-field-provenance.md)；未访问LOB、当前分配、整树和事务认证边界保持不变。

## 聚簇列投影入口

`ScanProjected/QueryProjected`及各自Auto/Materialized/MaterializedAuto变体提供单表聚簇投影。nil列列表选择全部存储列，显式空列表、重复/未知/VIRTUAL列拒绝；按请求顺序返回独立ProjectedRecord，Values及所有列索引辅助元数据均指报告Columns。节点和删除证据不裁列；普通Read/Scan/Query仍提供完整值。

页内全部字段和本地外部引用继续校验，只有所选LOB被物化，16MiB类型上限不变。扫描检查完整聚簇树两端及兄弟链，查询沿用KeyRange/PointKey/Prefix/Reverse/Limit。未选页外损坏或超限不阻碍合法投影，页内损坏仍失败；不认证未读页外值、事务或跨索引一致性。

元数据、导航、LOB共享context/缓存及累计预算；MaxRowBytes为整条本地记录加所选外部后缀和所选INSTANT默认编码。Complete/LimitReached、失败前缀及错误分类沿用既有扫描/查询规则，默认存在VIRTUAL仍拒绝，Materialized单独报告定义。来源重映射、真实字节、示例与验收见[第97章](97-clustered-projection.md)，不新增CLI参数或分区集合投影。

## 结构化校验与诊断入口

`Diagnose`接受可信单表Schema及可选Secondary定义，返回结构化Problem、分层Checks和累计计数，不返回可导入行。有效FSP/XDES识别使用中页后才检查其封套，free等残留不误报损坏；逐页独立收集问题，严格分配通过后检查所选索引局部值、完整导航/分配页覆盖及当前聚簇LOB。依赖损坏标为blocked，不把前缀或缺失范围当作通过；不做自动SDI诊断、MVCC/历史还原或跨索引一致性认证。

Complete表示执行未被操作错误中断，Passed仅表示实际请求范围通过；发现损坏/不支持可以Complete=true、Passed=false且返回nil error，须消费Problems/Checks及Unverified。I/O、取消、停止、预算中断返回原因并Complete=false，预算还标Truncated。Problem保留errors.Is/As链及已知页/索引/记录/当前逻辑列/字节位置；不可靠的位置省略，LOB实际页与所属记录页分列。

默认MaxPages=1000000、MaxIssues=1000，与ScanOptions的context、LRU和累计请求/遍历/行/源字节预算共用；空间复验/树复验不重置预算。SkipLOB只跳过链/类型，使用中LOB页的封套和分配仍校验；16MiB类型限制保留。状态、问题分类、真实字节及示例见[第98章](98-structured-diagnostics.md)。严格读取和导出仍失败无完整结果，正式CLI参数不变。

## 行列存储分析入口

`AnalyzeStorage/AnalyzeStorageMaterialized`接收可信单表Schema及ScanOptions，完整扫描聚簇树、当前存储值和LOB，失败返回nil报告。普通入口拒绝VIRTUAL，Materialized单列定义且不求值。每列分别报告声明编码容量、存储/默认/NULL/空值、编码/逻辑/本地长度及固定65桶；记录公共头、共享NULL位图、系统字段及DROP残留单列，保持本地记录字节的互斥分解。

LOB后缀按引用累计，当前块区间及引用首页/活动项索引页/数据页另行去重；跨列共享页只计全表Shared，列仅计Exclusive页容量。页容量不等于当前载荷，不认证FSP分配、MVCC、其他索引、历史值或可归还空间。类型值上限16MiB、累计扫描预算不变，整数溢出返回ErrLimit；JSON中的64位计数/桶使用十进制字符串。准确分母、真实119字节例子及验证见[第99章](99-row-column-storage.md)，不增加正式CLI参数。

## 聚簇与二级索引交叉校验入口

`CheckIndexConsistency`及Materialized变体接受可信单表Schema、至少一个显式SecondarySchema和可信导出证据。仅在Source非空、成功FOR EXPORT/持锁复制/目标写事务结束声明齐全且输入SHA256匹配后，按所选定义双向核对当前聚簇行与有效二级项；库不认证外部声明真实性。前提不满足为blocked且Passed=false，不凭普通副本、CRC或事务ID推断change buffer/提交状态。范围与源码依据见[第100章](100-index-consistency.md)。

比较覆盖支持的NULL/前缀/混合方向及聚簇定位副本，按现有_bin规则匹配；报告missing、extra、mismatch、duplicate_locator及适用唯一性矛盾。删除标记单列，Materialized单列未索引VIRTUAL定义，不恢复历史值。只物化索引相关列，未选LOB不读，16MiB类型限制不变。树/值验证失败阻断该索引缺项推断，其他所选索引可继续；不认证FSP分配、未选索引/LOB或SQL ReadView。

Complete与Passed分别消费，发现或无法认证可nil error且Complete=true、Passed=false。操作错误保留原因并Complete=false；预算标Truncated。所有索引、输入摘要及重复聚簇扫描共享ScanOptions预算，MaxRows包括扫描的当前行与有效二级项；默认保留问题1000条、每索引编码键64MiB，另外受累计行/遍历项限制，不承诺RSS。计数JSON为十进制字符串。不增加正式CLI参数、自动SDI或分区交叉校验。

## 统计汇总扩展

2026-10-12当前源码新增`stats.Build/WriteJSON/WriteTable`及CLI `stats`，提供summary、page-types、indexes、levels、segments、regions六视图和显式ID/level筛选；指标/精确协议见[第85章](85-statistics-model-and-metrics.md)，验证见[第86章](86-statistics-validation.md)。复用一次全文件AnalyzeSpace，认证分配、使用中页完整性和支持的局部布局；不认证SQL行数、schema相关树导航、列值或当前LOB引用。历史v0.1.0不包含此命令，须按最新提交重新编译。

## 列值、字符集与索引

<a id="mysql-5744-显式元数据扩展"></a>

### MySQL 5.7 显式元数据扩展

当前5.7.44可信元数据入口提供可信Schema JSON的ValidateSchema/Read/Scan/StreamLOB/聚簇Query及CLI `--schema`。历史v0.1.0不包含此扩展；准确版本由采集source证据/调用方提供，不能从任意.ibd自动识别补丁版本。格式/证据范围见[第87章](87-mysql57-explicit-schema.md)、[第88章](88-mysql57-secondary-indexes.md)与[二级验证报告](../mysql57-validation/secondary/report.json)。

2026-10-14新增明确补丁5.7.9、5.7.10、5.7.11、5.7.12、5.7.28、5.7.43，连同既有5.7.44形成采集白名单；没有放开中间未验收补丁。新增264份独立快照的组合与初版范围相同，源码差异、TLS构建限制和独立对照见[第92章](92-mysql57-patch-compatibility.md)。

| 维度 | 已验收5.7补丁的验证范围 |
|---|---|
| 文件 | 16 KiB、独立非压缩非加密、crc32、稳定快照；DYNAMIC/COMPACT，无SDI |
| 列值 | 既有数值/文本二进制/日期时间/ENUM/SET/JSON/二维空间类型代表组合、NULL/空值、四个受支持字符集；精度/宽度以固定样本为证据，不推定全组合 |
| 聚簇 | 整数键、三层VARBINARY/INT复合树、实际唯一非空键、隐藏DB_ROW_ID；点查/范围/反序/Limit |
| LOB/DML | 两格式旧BLOB，多列多页、完整更新/增长/缩短/DELETE快照；无undo或历史还原 |
| 生成列 | STORED物化；未索引VIRTUAL显式仅物化入口，默认完整读取拒绝 |
| 元数据与CLI | 实例采集Schema/SecondarySchema JSON；ValidateSchema/ValidateSecondarySchemas及metadata仅预检属性/根身份；metadata/query/check secondary/索引HTML接受配套--indexes |
| 二级/字符键 | 唯一/非唯一、NULL/重复、复合与隐藏定位、字符/二进制前缀、去重/完整定位重复、三层树、四个_bin规则；覆盖/回表/点查/范围/前导等值/反序/Limit；二级键类型组合限固定证据 |
| 空间工具 | page、space、stats、空间及完整所选索引HTML；继承既有分配/局部结构/所选树认证契约 |
| 后续边界 | 分区集合另按[第94章](94-partition-version-compatibility.md)扩展；未验收的二级类型/宽度/DDL/DML组合不作推断；无.frm自动发现、物理DESC/INSTANT/INVISIBLE、其他5.7补丁及原有非目标 |

上述版本分支不改变后文8.0.45已有类型/索引/API说明。5.7自动SDI入口明确拒绝；采集脚本还拒绝不支持的索引类型及隐藏物理列，显式Schema仍须可信。

### CLI输入准备扩展

2026-10-13当前源码新增`schema`和`manifest`，历史v0.1.0不包含。schema通过Go MySQL驱动（TCP/Unix socket、可选证书验证TLS）采集上述5.7补丁的单表可信Schema/可选SecondarySchema及来源，仅采集元数据，物理快照由备份工具提供；无Docker/Python/mysql客户端运行依赖。manifest从明确8.0补丁的SDI生成版本1清单；5.7通过schema --partitions和--partition-schema生成引用可信目录的版本2清单，完整性/DDL限制见[第94章](94-partition-version-compatibility.md)。不增加离线.frm/.cfg/系统字典发现或恢复范围。用法见[CLI指南](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#schema采集可信定义)，格式见[第91章](91-cli-input-preparation.md)，初版验收见[输入准备报告](../input-preparation-validation/report.json)，当前驱动连接验收见[驱动验证报告](../schema-driver-validation/report.json)。

### 既有 8.0.45 类型与键

| 列类型 | 返回与范围 | 限制 |
|---|---|---|
| TINYINT/SMALLINT/MEDIUMINT/INT/BIGINT | 对应Go整数宽度，含UNSIGNED完整范围 | 消费JSON数字须避免浮点丢精度 |
| DECIMAL | precision 1..65、scale 0..min(precision,30)，精确字符串 | 保留声明scale，不转float |
| FLOAT/DOUBLE | float32/float64有限值、次正规数及有符号零 | 不恢复写入前精度；不支持浮点索引键 |
| DATE/YEAR | 日期分量字符串；uint16年份 | 保留SQL零值/非日历分量，不自动归一化 |
| DATETIME/TIME/TIMESTAMP | 当前物理格式，fsp 0..6，保留声明精度 | TIMESTAMP输出UTC，不能恢复原会话时区；旧时间格式未实现 |
| BIT | 1..64位，uint64 | NULL与零分离 |
| CHAR/VARCHAR/TEXT家族 | UTF-8 Go字符串，TextBytes保留源字节 | CHAR Values去尾U+0020，CharStorage保留物理文本；CHAR 0..255，VARCHAR受字符宽度和声明限制 |
| BINARY/VARBINARY/BLOB家族 | 独立[]byte | BINARY 0..255，VARBINARY 0..65535；空字节与NULL分开 |
| ENUM/SET | 标签字符串及原序号/位掩码 | ENUM 1..65535项，SET 1..64项；字典序与空标签均保留 |
| JSON | JSONValue精确类型树、已知opaque解释及未知载荷 | SQL NULL与JSON null分离；16MiB、100层、100000节点 |
| GEOMETRY家族 | GeometryValue，SRID、WKB及七种二维几何结构 | 不转换坐标/轴序，不做拓扑或空间索引；16MiB、100层、100000节点、1000000坐标对 |

所有列均保留SQL NULL。声明容量不意味着任意列组合能被MySQL建表；完整类型物化的单值上限为16MiB，LONG类型不代表4GiB支持。独立`StreamLOB`可按预算读取超过16MiB的可信原始引用，不把它自动转换成超大类型值。

文本编码支持utf8mb4、utf8mb3（含schema别名utf8）、ascii、MySQL latin1。自动元数据映射仅覆盖collation ID 255/45/46、33/83、11/65、8/47；这些ID允许解码文本，不代表全部比较规则已实现。字符索引键仅支持utf8mb4_bin、utf8mb3_bin、ascii_bin、latin1_bin，按原编码及PAD SPACE语义比较；[49章](49-charset-storage.md)与[53章](53-typed-key-layout.md)分别解释解码和排序。

聚簇身份支持显式主键、实际唯一非空完整键、隐藏六字节DB_ROW_ID。显式键支持1..16成员、每成员ASC/DESC，类型为整数、CHAR/VARCHAR、BINARY/VARBINARY、DECIMAL、BIT及已支持日期时间类型；最大3072字节，COMPACT每成员最多767字节。聚簇前缀键和未支持collation拒绝。

普通BTREE二级索引支持唯一/非唯一、NULL、重复键、混合方向及受支持字符/二进制前缀。独立Secondary模型保留物理前缀和聚簇定位字段；查询比较完整原列，保守筛选前缀候选并按需回表。不实现FULLTEXT、SPATIAL、多值或函数索引，也不把这些索引当普通BTREE解码。

LOB支持LOB_FIRST/LOB_DATA/LOB_INDEX、已验收的当前活动版本，以及旧BLOB链。COMPACT保留本地前缀，按实际页类型选链；历史链用于结构校验，不输出历史值，修改进行中或不支持的历史引用拒绝。

## 请求入口与成功含义

| 需求 | Go / CLI入口 | 成功范围 |
|---|---|---|
| 元数据 | ReadSDI、InspectTable、InspectSecondary / metadata；schema采集可信5.7定义 | 对象、schema及入口报告；Issues不能忽略 |
| 原始/校验页 | ReadRawPage、ReadPage / Go库入口 | 指定页的原始字节或CRC/LSN/页号封套；未知内部类型不解码 |
| 本地索引页 | ReadClusteredPage、ReadSecondaryPage / Go库入口 | 页0和目标页局部布局；未解引用值、删除证据与完整行区别表示 |
| 字段物理来源 | TraceClusteredPage、TraceSecondaryPage / Go库入口 | 本地载荷、元数据位/区间、默认来源及DROP/系统字段；不隐式读取LOB |
| 完整当前行 | Read/ReadAuto及Materialized变体 / export、check rows | 受支持聚簇树及请求的当前值；Read失败不返回部分结果 |
| 逐行消费 | Scan及Auto/Materialized变体 | 回调可能已有前缀，必须同时检查error和Complete |
| 聚簇点查/范围 | Query及Auto/Materialized变体 / query | 索引顺序边界、前导列Prefix、Reverse、Limit；不认证未访问子树 |
| 聚簇列投影 | ScanProjected、QueryProjected及Auto/Materialized变体 / Go库入口 | 按请求列序输出，未选LOB不读；扫描完整树或查询选中路径的投影 |
| 二级物理项 | ReadSecondary、ScanSecondary及Auto变体 / check secondary | 独立二级树，非完整用户行，不自动回表 |
| 二级投影 | QuerySecondary及Auto/Materialized变体 / query | 覆盖/按需回表；未选LOB不读取，预算跨路径累计 |
| 完整分区集合 | InspectPartitions、ScanPartitions及Materialized / --manifest；manifest生成清单 | 预检全部文件、定义序扫描；后段失败仍可已有输出前缀 |
| 空间 | AnalyzeSpace / space、check space、page | 分配/使用中页/局部结构；不等同键顺序或完整字段校验 |
| 统计（后续源码） | stats.Build、WriteJSON/WriteTable / stats | 六视图仅聚合空间报告；筛选仍校验全文件，任一错误无完整报告 |
| 离线图（独立应用） | CLI visualize；应用内部visual.Build、WriteHTML | 默认空间；显式选索引完整扫描，所选页嵌入详情；请求层失败不降级；不再是本库公开子包 |
| 原始大LOB | StreamLOB | 按引用与预算的当前原始字节流，不解释为SQL值 |
| 结构化诊断 | Diagnose / Go库入口 | 问题/依赖/范围报告；Complete与Passed分开，不提供数据恢复 |
| 行列存储分析 | AnalyzeStorage及Materialized / Go库入口 | 精确当前列载荷、公共记录开销和去重引用页容量，失败无部分报告 |
| 索引交叉校验 | CheckIndexConsistency及Materialized / Go库入口 | 在可信且绑定输入的导出证据下双向校验所选索引，缺失前提为无法认证 |

`ErrUnsupported`表示未支持输入，`ErrCorrupt`表示可检测矛盾，`ErrLimit`表示预算，`ErrStopped`表示回调主动停止；使用`errors.Is`分类，并保留底层I/O/context错误。显式schema必须可信，解析器不能发现所有逻辑定义错配。ReaderAt稳定性、打开/关闭与回调保留数据由调用方负责，取消不保证打断任意底层阻塞I/O。

CLI退出0/2/1/130分别代表成功/参数错误/运行失败/取消；stdout为数据，stderr为诊断。JSONL必须读到end并核对行数和尾随EOF；CSV采用带类型单元格协议，EOF不证明生产成功。单文件输出仅成功后原子发布，默认不覆盖；schema目录逐个发布完整文件，须检查最终状态；显式overwrite仍保护输入身份。stdout失败前缀不可撤回，目录断电持久性不作承诺。

## 预算与内存

| 层级 | 默认值/固定限制 | 计量说明 |
|---|---|---|
| Scan/Query | 缓存64页；页请求/遍历项/行各1000000；单行源字节64MiB | 命中缓存仍计页请求；二级回表和分区共享累计预算 |
| StreamLOB | MaxLOBBytes 64MiB | 可显式配置；类型物化16MiB上限保持 |
| SDI | 单对象16MiB、累计解压64MiB、4096对象 | 独立容量限制 |
| 分区集合 | 最多1024文件、集合SDI JSON64MiB；CLI清单1MiB | 清单相对路径以清单目录为准 |
| AnalyzeSpace | 1000000页、10000000遍历项 | 不物化用户行 |
| Diagnose | 1000000文件页、1000问题；其余沿用ScanOptions | 所有阶段共享计数；LOB可显式跳过链/类型，使用中页封套仍检查 |
| AnalyzeStorage | 沿用ScanOptions，类型值16MiB | 同次解码收集来源；每列固定65桶，LOB页/区间去重；不保留全表行 |
| CheckIndexConsistency | 沿用ScanOptions；1000问题、每索引64MiB保留编码键 | 摘要及所有索引共用预算；不保留全行，不物化未选LOB |
| stats（后续源码） | 1000000页、10000000空间遍历项 | 继承AnalyzeSpace计量；保留全页分析内存，不引入行扫描或RSS硬上限 |
| visualize | 默认100000页、实际ReadAt累计1000000次、详情10000条、嵌入JSON64MiB；最多256个原始页 | 显式索引另沿用扫描预算；界面分页不代表抽样 |
| rowio读回 | 每条记录128MiB | 字节协议限额，不是进程内存限额 |

这些限制均不是RSS硬上限。Read保留整表结果；Scan按行交付，但调用方保留记录仍会增长内存。性能/内存实验及其限制见[第84章](84-release-validation.md)。
