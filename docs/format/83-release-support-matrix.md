# 83. 首版支持矩阵与入口选择

## 基线与判断方法

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

2026-10-13当前源码新增`schema`和`manifest`，历史v0.1.0不包含。schema通过Go MySQL驱动（TCP/Unix socket、可选证书验证TLS）采集上述5.7补丁的单表可信Schema/可选SecondarySchema及来源，仅采集元数据，物理快照由备份工具提供；无Docker/Python/mysql客户端运行依赖。manifest从明确8.0补丁的SDI生成版本1清单；5.7通过schema --partitions和--partition-schema生成引用可信目录的版本2清单，完整性/DDL限制见[第94章](94-partition-version-compatibility.md)。不增加离线.frm/.cfg/系统字典发现或恢复范围。用法见[CLI指南](../CLI.md#schema采集可信定义)，格式见[第91章](91-cli-input-preparation.md)，初版验收见[输入准备报告](../input-preparation-validation/report.json)，当前驱动连接验收见[驱动验证报告](../schema-driver-validation/report.json)。

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
| 完整当前行 | Read/ReadAuto及Materialized变体 / export、check rows | 受支持聚簇树及请求的当前值；Read失败不返回部分结果 |
| 逐行消费 | Scan及Auto/Materialized变体 | 回调可能已有前缀，必须同时检查error和Complete |
| 聚簇点查/范围 | Query及Auto/Materialized变体 / query | 索引顺序边界、前导列Prefix、Reverse、Limit；不认证未访问子树 |
| 二级物理项 | ReadSecondary、ScanSecondary及Auto变体 / check secondary | 独立二级树，非完整用户行，不自动回表 |
| 二级投影 | QuerySecondary及Auto/Materialized变体 / query | 覆盖/按需回表；未选LOB不读取，预算跨路径累计 |
| 完整分区集合 | InspectPartitions、ScanPartitions及Materialized / --manifest；manifest生成清单 | 预检全部文件、定义序扫描；后段失败仍可已有输出前缀 |
| 空间 | AnalyzeSpace / space、check space、page | 分配/使用中页/局部结构；不等同键顺序或完整字段校验 |
| 统计（后续源码） | stats.Build、WriteJSON/WriteTable / stats | 六视图仅聚合空间报告；筛选仍校验全文件，任一错误无完整报告 |
| 离线图 | visual.Build、WriteHTML / visualize | 默认空间；显式选索引完整扫描，所选页嵌入详情；请求层失败不降级 |
| 原始大LOB | StreamLOB | 按引用与预算的当前原始字节流，不解释为SQL值 |

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
| stats（后续源码） | 1000000页、10000000空间遍历项 | 继承AnalyzeSpace计量；保留全页分析内存，不引入行扫描或RSS硬上限 |
| visualize | 默认100000页、实际ReadAt累计1000000次、详情10000条、嵌入JSON64MiB；最多256个原始页 | 显式索引另沿用扫描预算；界面分页不代表抽样 |
| rowio读回 | 每条记录128MiB | 字节协议限额，不是进程内存限额 |

这些限制均不是RSS硬上限。Read保留整表结果；Scan按行交付，但调用方保留记录仍会增长内存。性能/内存实验及其限制见[第84章](84-release-validation.md)。
