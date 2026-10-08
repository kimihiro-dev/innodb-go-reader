# 项目约定

## 当前阶段

- `REQUIREMENTS.md`、`DECISIONS.md`、`TODO.md`、本文件为项目状态来源。
- 使用中文记录分析；区分源码事实、README 声明、实测结果与建议。
- 引用源码时使用文件路径和行号；不修改参考目录。
- 不将 MySQL 密码写入文档、测试文件或提交内容。
- 第一阶段仅使用 Go 标准库；根包 `innodb`，模块名 `innodb-go-reader`，使用 gofmt。
- API 输入使用 `io.ReaderAt`，调用方管理文件生命周期。列值为按宽度对应的 Go 有符号/无符号整数、float32/float64、string、[]byte、JSONValue、GeometryValue 或 nil（MEDIUMINT 用 int32/uint32，旧 INT 保持 int32），保留物理元信息，Read系列错误时不返回部分数据；流式接口的部分输出协议见第32阶段约定。
- 底层错误以 `%w` 包装；可检测损坏和不支持布局分别用 ErrCorrupt/ErrUnsupported，测试用 errors.Is 检查。
- 固定夹具测试离线运行，SQL 预期值独立生成；原始文件通过 SHA256 校验，损坏测试仅修改内存副本。
- 多页结果中 Record.PageNumber 标明来源，Start/Offset/End 仍为页内偏移；Result.Page 始终为根页，Pages 为 DFS 访问顺序，Nodes 与用户 Records 分开。
- 较大夹具用 gzip 无损保存，SHA256 对解压后的 .ibd 计算；gzip 解压由调用方处理，不混入物理页解析。
- 后续物理文件测试应记录生成 SQL、MySQL 版本、配置、快照过程和期望结果；运行中实例的文件读取不作为一致性基准。

## 解析手册约定（用户已要求）

- 每个实现小步骤同步完成一章或一节中文解析文档，未完成文档不视为该步骤完成。
- 章节包含：学习目标、前置概念、结构图、字段偏移/长度/字节序/含义、真实夹具字节、逐步解码、代码入口、验证方法和边界。
- 严格区分文件绝对偏移、页内偏移、记录起点相对偏移；整数编码、字符字节数与 NULL 等易混概念必须通过例子解释。
- 真实十六进制片段必须提取自交付夹具；未实测的例子标为示意，不伪造测试输出。
- 用同一组贯穿案例把各章串起来，目录明确已实现、计划中及不支持范围。
- 不以“有代码”替代格式解释，也不要求读者先读 Java 项目才能理解 Go 实现。

- 第三阶段类型矩阵见 [第三阶段计划](STAGE_PLANS.md#stage-3)；NodePointer.Key 与主键列返回类型一致，使用 any 承载。JSON 大整数用 UseNumber 精确读取；禁止测试通过 float64 中转整数预期。

- 第四阶段支持矩阵见 [第四阶段计划](STAGE_PLANS.md#stage-4)：VARBINARY 的 max_bytes 与 VARCHAR 的 max_chars 分离；二进制独立复制，空 []byte 不为 nil，JSON 使用标准 Base64。SQL 二进制预期用 HEX 对照。
- 页内 garbage 包括复用空闲记录留下的碎片；free 为空不要求 garbage 为零，活动记录总长度须与堆顶及 garbage 一致。

- 第五阶段支持矩阵见 [第五阶段计划](STAGE_PLANS.md#stage-5)；Record.External 的 Offset 为聚簇页内引用起点，Chunks 为逻辑顺序的数据块，第五阶段的 IndexOffset 位于首个 LOB 页。Record.End 仍只描述本地记录，Result.Pages 仍只含聚簇页。
- 第四阶段 external 夹具原始资产与历史 manifest 保留不改，第五阶段测试将其作为成功 SQL 对照；不能再把 expected_error 历史标记当作当前拒绝契约。

## 阶段计划维护

- 所有阶段计划统一维护在 [STAGE_PLANS.md](STAGE_PLANS.md)。新阶段追加到文档末尾，使用连续的 stage-N 锚点，更新目录与状态，不再创建独立阶段计划文件。
- 各阶段保留当时的范围和验收标准；最新支持范围以 REQUIREMENTS.md 为准，任务状态同步更新 TODO.md。

- 第六阶段：TEXT/BLOB 类型容量使用 uint64，禁止自定义长度和 unsigned；单值实现上限为 MaxLOBValueBytes（16 MiB）。LOBChunk.IndexPage 标识索引项所在页，IndexOffset 相对于该页，Chunks 仍按逻辑顺序排列。新夹具 long_over_limit 是当前有效的超限拒绝契约。

- 第七阶段：DECIMAL 使用显式 precision/scale，返回保留 scale 的 Go string / JSON 字符串（包括 scale=0），不通过浮点转换。类型固定字节宽度，不消费变长元数据；SQL 预期使用 CAST AS CHAR。非零负 UNSIGNED 值报 ErrCorrupt，零统一非负；主键仍仅限整数。

- 第八阶段：FLOAT/DOUBLE 分别返回 float32/float64，有限 IEEE 754 小端位模式原样还原（包括负零）。JSON 数字按目标列位宽解析比较；不要先把 -0 当整数读取。非有限编码或非零负 UNSIGNED 值报 ErrCorrupt，precision/scale 仍仅用于 DECIMAL。

- 第九阶段：DATE 返回保留原始年月日分量的字符串，不使用 time.Time 归一化；YEAR 返回 uint16，零与 NULL 分开。DATE/YEAR schema 不接受 unsigned/长度/精度属性；日期主键仍不支持。特殊日期夹具的会话 sql_mode 必须记入生成 SQL 与 manifest，不修改全局模式。

- 第十阶段：DATETIME 使用独立 fsp=0..6，输出保留 fsp 位的无时区字符串；该阶段其余类型的 fsp 必须零（第十一阶段扩展 TIME）。按 DATETIME2 固定二进制格式读取，不归一化日期，不应用时区，不复用 DECIMAL precision/scale。小数秒用整数运算并验证容量/精度对齐，SQL 预期 CAST AS CHAR。

- 第十一阶段：fsp 同时适用于 DATETIME/TIME，其他类型必须零。TIME 返回带符号且保留精度的时长字符串，小时不按 24 取模；零不带负号。校验正负小数借位/范围/对齐及 ±838:59:59 端点，SQL CAST AS CHAR 对照。

- 第十二阶段：fsp 适用于 DATETIME/TIME/TIMESTAMP；TIMESTAMP 输出固定 UTC 字符串，保留 fsp，零秒零小数是 MySQL 零值，NULL 独立。正常秒数 1..2147483647；零秒非零小数明确拒绝。主 SQL 预期以 +00:00 导出，显示时区证据独立保存，严禁依赖 time.Local。

- 第十三阶段：BIT 使用独立 bit_length=1..64，返回 uint64，NULL 为 nil；其他类型 bit_length 必须零。按定长大端解码，未使用高位非零报 ErrCorrupt；不复用 unsigned/长度/精度参数。SQL CAST AS UNSIGNED 配合 UseNumber 对照，保留 64 位精度，BIT 主键不支持。

- 第十四阶段：BINARY 的 max_bytes=1..255 表示精确固定宽度，不参与变长长度数组。返回独立 []byte，保留尾部零/空格及任意二进制；NULL 独立，JSON 沿用 Base64。SQL HEX 对照，不猜测补零前输入长度。BINARY(0) 与二进制主键未支持。

- 第十五阶段：ENUM 使用实际存储顺序的 UTF-8 enum_values，1..65535 项；Values 返回 string/nil，EnumIndexes map[int]uint16 以逻辑列下标保存非 NULL 原序号。查询 map 必须判断键存在，区分 NULL、零号错误值与合法空标签。固定一/两字节，不参与变长数组；SQL 标签和数值序号分别对照，示例仅输出标签。

- 第十六阶段：SET 使用独立 set_values（1..64 项 UTF-8、无逗号），固定 1/2/3/4/8 字节，大端掩码。Values 返回 SQL 风格字符串/nil，SetMasks map[int]uint64 保存非 NULL 掩码，判断存在性以区分 NULL/零。空标签分隔遵循 MySQL 输出缓冲规则；SQL 字符串、掩码与混合 ENUM 序号独立对照。

- 第十七阶段：utf8mb4 CHAR 使用 max_chars=1..255，参与变长长度数组；完整页内/页外文本验证 N..4N 字节及 UTF-8/字符数。Values 仅裁剪末尾 U+0020，CharStorage 按逻辑列下标保留非 NULL 物理文本，NULL 无项。不推断用户原输入空格数量，不影响 VARCHAR/TEXT。

- 第十八阶段：readPage 对完整16 KiB页验证普通 CRC32C 双区间异或、头尾CRC和LSN低32位；CRC失配按严格输入契约报 ErrCorrupt，不猜测算法。仅检查访问路径。结构损坏测试在内存副本重封装CRC，校验损坏测试禁止重封装，原始资产不可修改。

- MySQL 8.0.45官方源码优先从 `/Users/kimihiro/workspace/codespace/cpp/mysql-8.0.45` 只读核查（用户提供，MYSQL_VERSION已核对）；文档使用相对此源码根的路径与行号。
- 第十九阶段：ReadSDI返回原始json.RawMessage及(type,id)/物理来源，不生成用户schema。SDI_BLOB链与用户LOB分开；对象压缩/解压各16MiB、累计解压64MiB、4096对象上限。官方ibd2sdi预期离线保存，用UseNumber对照，原始资产不改；多层合成测试须与真实夹具明确区分。

- 第二十阶段：InspectTable报告原DD属性、索引入口与Issues；Issues非空不提供Schema。ReadAuto复用Read，错误不返回部分行。自动字符映射先限collation255/63；RootVerified仅表示入口页核对，不承诺二级记录解析或事务一致性。生命周期测试默认验证testdata/metadata真实快照，INNODB_METADATA_FIXTURES可选择新采集目录，缺失报错；合成搬根/二级报告测试不得称为真实索引变更验收。

- 第二十一阶段：JSON列返回JSONValue类型树，SQL NULL为nil、JSON null为kind=null节点；JSON()输出普通视图，默认JSON序列化保留类型树。保留BinaryType、整数精度/浮点负零、opaque类型与独立载荷；JSON temporal按packed字段显示，不应用普通TIMESTAMP列的UTC秒数规则。最大16MiB/深度100/100000节点，初始连续布局；未知opaque保留原字节，不将未知外层标签当合法opaque。SQL预期数字精确比较，真实与合成证据分开。

- 第二十二阶段：空间列返回GeometryValue（SRID、独立WKB、Geometry树），SQL NULL为nil；Points/Rings/Geometries按类型组织，ByteOrder逐节点保留。保持存储轴序，不查SRS或转换CRS；Column.SRID指针区分未声明与SRID0，具体类型/SRID不符拒绝。16MiB/100层/100000节点/1000000坐标对保护，损坏无部分返回；真实小端与合成大小端证据分开。

- 第二十三阶段：Charset省略为utf8mb4，utf8别名按utf8mb3，另支持ascii/MySQL latin1；Values统一UTF-8，TextBytes保存非NULL CHAR/VARCHAR/TEXT独立字节，CharStorage保存解码后含残留空格文本。单字节正宽CHAR定长，UTF8和零宽CHAR变长；CHAR/BINARY/VARCHAR/VARBINARY允许经实测的零宽非NULL值，不能由最大长度零推断无长度数组。仅九个已验收collation，字典原字节按对应编码解码；未实现排序比较。历史阶段的零宽/字符集限制由本阶段矩阵扩展。

- 第二十四阶段：PrimaryKey与PrimaryKeys非空时互斥，1..16升序非NULL整数成员按索引定义序；单列自动schema仍使用旧字段，多列使用数组。Values保持SQL列序；NodePointer.Key单列标量、多列[]any，成员各自精确类型。内部元组逐位比较，nil范围为无界，MIN_REC继承下界；完整重复键拒绝。历史单列限制由本阶段扩展，字符串/DESC仍未实现。

- 第二十五阶段：Column.Collation只用于CHAR/VARCHAR键，且必须为匹配Charset的四个_bin之一；Descending仅用于键成员。完整非NULL键1..16成员、声明总宽度<=3072、声明非零。叶子长度按键优先物理序，非叶子只读键长度；原始键副本用于PAD SPACE/字节序及逐成员方向比较，不使用Values中的显示字符串。NodePointer.Key保持原值类型；旧整数键限制由第53–54章矩阵扩展。

- 第二十六阶段：新增Schema.ClusteredKey（Name为SDI索引名、Columns为唯一聚簇成员或HiddenRowID=true），与旧主键字段互斥。Record.RowID *uint64仅隐藏布局有值，六字节大端无符号，Values不混入系统列；隐藏导航Key为uint64。允许普通BTREE二级索引共存，仅报告/验证根，不解析二级记录。实际首个聚簇索引需与完整字段布局核对，不按名称/支持类型改选候选。无指定SQL顺序用多重集合，保留重复次数；旧二级拒绝历史标记不能当作当前契约。

- 第二十七阶段：Records仅含未delete-mark物理行，DeletedRecords为独立摘要（Key/RowID、页/偏移/头、事务字段与独立Raw本地字节），没有Values且不跟随删除引用。在链删除记录仍计入Page.Records并参与目录/堆/garbage/全键范围校验；free残留不输出为行。受控写事务已结束的稳定快照才对照SQL当前行，不推断MVCC/提交状态，复杂更新LOB仍未支持。

- 第二十八阶段：当前LOB引用与FIRST版本相等；活动块允许旧版本，历史链验证但不输出历史值。允许non-owner/inherited及FIRST禁用部分更新标志，修改中或旧引用仍拒绝；Chunks只含当前块来源。JSON允许无重叠空洞/重排，不解释残留字节，类型树不变。对应真实夹具SQL预期直接保存查询文本，精确数字不得经浮点中转；旧初始LOB/连续JSON限制由本阶段扩展。

- 第二十九阶段：Schema.Instant保存当前Version和用户物理Fields（含DROP类型），Position包含键/系统位置，Column引用当前列或-1；Default保存ADD时InnoDB字节/NULL。Record.RowVersion仅显式版本字节，DefaultColumns标明字典补值来源。每行按生命周期计算字段与NULL位图，非叶子按版本0；DROP载荷仅定位不解码/追LOB，Values保持当前SQL列序。原生行版本1..64与无版本初始行支持，旧式/升级布局仍拒绝。

- 第三十阶段：Schema.Columns仅列当前物化列，VirtualColumns保存未物化用户列Name/Ordinal（从1起、排除系统和DROP列）/Expression/Invisible。Column.GenerationExpression只描述STORED，Invisible不取消存储。Read/ReadAuto遇VIRTUAL拒绝；ReadMaterialized/ReadMaterializedAuto返回Columns、VirtualColumns和Result，Values及所有列号元信息使用物化Columns下标，不返回VIRTUAL占位nil。Issues非空时Schema仍nil，只有用户VIRTUAL问题时MaterializedSchema可用；其他不支持布局不能绕过。

## 第31阶段 COMPACT 与旧 BLOB

Schema.RowFormat省略表示DYNAMIC，COMPACT显式指定并与SDI/空间标志核对。行格式只控制本地768字节前缀，实际首个页类型决定新LOB/旧BLOB读取。ExternalField.Offset定位20字节Reference；Prefix独立持有原始字节；Length是后缀长度，16MiB预算包含前缀。旧链Format为BLOB、HeaderOffset=38、Version=0，Chunks的IndexPage/IndexOffset为0；新链保持原版本契约。完整拼接后再解码类型。普通与debug写出夹具分别保存，SQL与官方SDI是独立基准，physical/verification是派生结果，不混淆旧链测试与旧版本兼容。

## 第32阶段流式接口

Scan/ScanAuto和显式ScanMaterialized/ScanMaterializedAuto共享walkTree，与Read保留同样校验；Read继续错误返回nil。ScanEvent单次只含Page/Node/Record/DeletedRecord之一，同步按DFS/键序交付，事件可独立保留/修改，输入schema和快照须保持稳定。只有nil错误且报告Complete才完整；计数包含返回错误的回调，已输出前缀不可撤回。ErrStopped为主动提前结束，ErrLimit为预算耗尽，context和I/O错误可用errors.Is。context不能打断任意阻塞ReaderAt/回调，不关闭调用方文件。

零值ScanOptions使用64页缓存、100万页请求/遍历项/当前行、64MiB单行源字节及独立LOB原始字节预算；缓存-1禁用。请求包含命中，累计遍历项限制任务/记录/LOB状态；单行源字节不等于Go堆，预算不承诺进程RSS。SDI保持已有独立容量限制。Read不自动套用流式预算。

StreamLOB只使用ExternalField.Reference/Prefix，调用方提供可信来源；内部重新解析引用，原始块保留逻辑偏移与Chunk来源。分块可能跨字符/JSON边界，不做完整类型/MVCC/记录归属验证。独立原始流可显式调整字节预算，类型物化仍最多16MiB。新旧LOB共用块级校验核心，缓存淘汰复用载荷缓冲；不保留全部LOB索引页载荷。内存实验区分GC后采样存活堆、B/op累计分配和RSS；原有超限资产在类型入口仍拒绝，独立块流成功不修改原资产拒绝属性。

## 第33阶段聚簇键查询

Key按实际聚簇成员顺序列值；整数只接受Go整数/json.Number，二进制为[]byte，字符为UTF-8，DECIMAL/时间为既有规范字符串。严格范围、字符集及精度验证，不经float64、不模拟SQL隐式转换；隐藏ROW_ID限单个48位值。查询开始时独立复制归一化键。

KeyRange上下界为完整键，按每成员ASC/DESC索引顺序；nil无界，倒置/相等开放区间为空。Prefix是非空完整前导成员等值，与边界互斥，不是LIKE。Reverse仅改变实际访问/输出方向。Limit=0无限制，成功交付指定当前行数后Complete/LimitReached为true，不推断还有剩余行；删除摘要不占Limit。QueryReport的Complete只认证请求范围/limit与访问路径，错误/取消/主动停止/预算耗尽仍不完整，已交付前缀不可撤回。

四个Query入口沿用严格/仅物化视图与ScanEvent/ScanOptions。Page/Node是路径事件，不保证导航键在范围内。目录二分在本地完整解析后进行，子范围剪枝减少I/O；访问页完整本地校验、所访问相邻页链和跨页顺序检查，范围外LOB不跟随，不声明未访问页已验证。无界Query不替代Scan整层链首尾检查。显式schema下可证明为空的请求不读文件，Auto仍先查SDI。

SQL查询基准显式指定与键一致的字符集/排序规则，混合ASC/DESC用逐成员谓词展开；保留原始SQL文本、独立结果和不可修改快照。错误collation探针是诊断证据，不得作为正确性预期。性能报告区分聚簇Page事件、PageReads含缓存请求、PhysicalReads和Auto元数据开销。查询CLI用UseNumber及明确base64对象区分二进制与文本。

## 第34阶段二级物理记录

SecondarySchema.Fields为实际物理序，UserFields为声明成员数，ClusteredFields映射完整聚簇键顺序。SecondaryField.Column指向当前物化表列（ROW_ID为-1），Definition保留原列类型和本成员方向，PrefixBytes为声明最大字节数，0表示完整字段；前缀标记不证明某条原值实际被截短。SDI和内部字典的去重规则不同，须由二级字段和聚簇键重建实际布局；同列前缀/完整副本分别保留，已有完整字段复用。

SecondaryRecord.Values/FieldBytes按Fields排列，nil仅为SQL NULL，零长字节独立；保留Raw/页内来源/ClusteredKey/RowID/DeleteMarked，没有虚构的事务字段。Nodes和DeletedRecords独立；前缀仅支持CHAR/VARCHAR/BINARY/VARBINARY，原列容量决定长度头格式，缩短后类型用于字段范围核验。ASC NULL最小、DESC反向；完整物理键严格递增，唯一用户键仅对全部非NULL且未delete-mark记录额外约束。

InspectSecondary按准确索引名读取SDI，采用MaterializedSchema映射当前物化列，不求值非索引VIRTUAL，索引VIRTUAL仍拒绝。ReadSecondary系列错误返回nil；ScanSecondary系列复用同步事件/取消/预算/Complete，报告独立，MaxRows计当前二级项，MaxRowBytes计本地源字节。全选定树验证包含首尾链、父范围与跨页顺序，不认证其他索引或SQL事务可见性。SQL验证区分当前记录、受控DML推导的删除键集合和不可由SQL访问的隐藏ROW_ID，后者单独与聚簇物理身份核对。

- 第35阶段：SecondaryQuery边界是完整二级声明列值，nil成员表示索引NULL，首个前缀字段之后保守放宽候选；完整副本可先过滤，缺值才回表。Columns=nil选全部物化列，显式空/重复/未知/VIRTUAL拒绝。ProjectedRow独立返回投影与来源，覆盖不认证未访问聚簇树。回表必须核对当前行和二级字段；仅解引用谓词/投影所需LOB，公开Record完整值契约不变。Auto发现、二级、每次聚簇点查与LOB共享缓存和累计预算；MaxRows计交付，Limit计成功回调，完整范围/Limit成功与提前错误严格区分。

## 第36阶段空间分析

AnalyzeSpace使用独立SpaceOptions，严格错误返回nil报告，无部分成功。默认MaxPages=1000000、MaxEntries=10000000，分别限制输入文件页和累计结构遍历；不物化用户行/LOB。先依据FSP/XDES分配证据区分used/free/uninitialized/file-tail，四类总和等于FilePages；ZeroPages是重叠观察量。只有使用中页进入CRC/LSN/FIL及结构/归属认证，空闲残留头不代表当前对象。未知使用中页保留独立Raw并明确内部未知。

Extent.SegmentID保存XDES原ID，仅state4/5表示当前归属；租用区State5的前2个公共页不计入Segment使用/保留量，但FSEG_NOT_FULL_N_USED按官方描述Used汇总核对。索引根可能为叶页，仍属于top段；LOBPages只表示段内类型归属。INDEX链/heap/owned可无schema验证，字段长度、键顺序、父子导航和LOB版本留给对应完整接口。LayoutUsedBytes=HeapBytes-GarbageBytes是头部推导量，分母必须显式，不合并成一个填充率。

空间大样本用.space.gz与独立SQL/官方证据，Go分析报告只能留档不能作为独立预期。官方innochecksum dump不列Other类型逐页数据，应分别验证已识别页和Other汇总，勿认定新式LOB缺页。既有.ibd资产不改，仍按原逐行回归基线执行。

## 第37阶段正式CLI与rowio

正式命令只复用公开解析API，main仅信号/退出；stdout数据与stderr执行摘要分离，0/2/1/130分别成功/参数/运行错误/取消。check必须标注rows/space/secondary范围，page先检查完整空间；VIRTUAL保持显式materialized。预检页读取单独报告且扣除总请求预算，SDI容量限制仍独立。

rowio版本1共用带类型值协议；JSONL要求schema/row/end及尾随EOF，CSV首记录schema、后续固定宽度JSON单元格且无完成尾行。CSV EOF不等于生产成功；两者每物理行一记录、要求终止换行，默认128MiB/记录。整数/浮点文本保持位宽及精度，JSON树不经float64，WKB重建空间树；physical是RawMessage，非类型map读取须UseNumber。协议验证不代替外部schema合法性认证。

--output同目录临时文件、成功编码/Sync/Close后发布，默认Link无覆盖、显式overwrite才Rename；保护输入和查询文件身份，失败清理本次临时文件。只承诺单文件原子可见性，不承诺目录断电持久性或强制打断阻塞I/O。夹具只读，子进程与失败实验使用临时副本。

## 第38阶段分区集合

PartitionInput显式命名分区，输入次序与路径不决定输出顺序。只接受完整普通独立分区集合；唯一Table SDI在number=0文件，其余文件可仅含Tablespace。逻辑列所有者指向首分区，分区物理表/index/root分别验证；不能用移除矛盾字段来适配。InspectPartitions错误返回nil，ScanPartitions先全部预检，再Metadata及带独立Source的ScanEvent，逐分区定义序和各自聚簇序；Complete/已输出前缀/显式Materialized规则沿用流式契约。

共用scanReader累计页/目录/index_opx/用户树遍历/行预算，切换文件清空缓存；SDI树沿用独立容量限制。最多1024文件、集合SDI JSON64MiB、单对象16MiB。CLI清单UTF-8/version1最多1MiB，相对路径基于清单目录，仅metadata/export/check rows支持；导出physical总带partition，--physical再带record，Values不加伪SQL列，rowio版本不变。输出保护全部输入及清单。

分区夹具用.partition.gz单独计数，不加入旧单文件自动读取glob。保存逐分区有序SQL与全表多重集合、官方CRC/SDI证据；MySQL8.0.45 INNODB_INDEXES没有本样本分区物理索引行，索引身份以官方ibd2sdi独立核对，不冒称SQL索引对照。身份一致不能证明不同DML时刻的事务一致性；调用方保证稳定采集窗口。

## 第39阶段离线可视化

visual.Build只编排公开AnalyzeSpace/InspectTable/Scan/ScanSecondary API，不新增物理解码器；默认空间层，显式单索引完整树及所选页记录/LOB来源，任一请求层失败返回nil，不降级。未知页原始字节也仅随显式Pages嵌入；不保留完整Values/LOB载荷，ExternalField的本地Prefix与Reference保留真实来源。树边来自导航事件，同层链独立；普通字段偏移无API依据就不猜。

报告JSON全部数字编码为十进制字符串，LSN/ID用字符串或BigInt；HTML通过template和JSON安全转义，动态文本只用textContent，自包含无网络。visualize复用原子输出和输入保护；空间/源读取/索引扫描/详情数/JSON体积分别有明确预算，超限失败，界面分页不代表抽样。Build与WriteHTML的上限不承诺RSS，Reader稳定性和生命周期仍由调用方保证。

图中指标必须逐项对照API，字节必须对照原始固定快照，LSN明确不是实时热点，LayoutUsedBytes/16384只是整页分母的物理布局占用。自动数据/编码测试、用户浏览器反馈、工具浏览器自动验收三种证据分开记录；本阶段工具file://被拒绝，关键交互由用户明确确认。


## 首版冻结与后续变更

2026-09-30首版当前范围以手册83的矩阵与84的证据入口为准；旧章节和历史决策保留当时边界，不把历史“尚未支持”当成当前限制。未来增加版本、页大小、类型、布局或协议必须更新矩阵、决策、独立预期和复验记录，不能仅凭已有类型测试推定新组合成功。

复验使用scripts/verify_release.py，输出新目录、保留日志与complete状态、检查源码/夹具指纹。缓存结果与实际新执行、历史官方/SQL证据与本次离线校验、用户浏览器反馈与自动视觉验收必须区分。基线日期不是SemVer兼容承诺，未授权不创建远程发布。
