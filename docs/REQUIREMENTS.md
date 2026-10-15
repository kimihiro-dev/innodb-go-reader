# 项目需求

## 问题与目标

用 Go 离线读取受支持的 MySQL InnoDB物理文件，完整还原当前存储列值，并提供可复核的中文解析手册和供上层应用调用的基础库。

已确认的数据还原优先原则：不将不支持的数据静默省略、填零或替换为 SQL NULL；每项解析能力随实现交付格式解释和独立验证，不逐类移植 Java 参考项目。

## 当前交付状态

- 当前仓库提供基础库及[01–100章解析手册](format/README.md)；十个命令由独立CLI项目提供，操作见[CLI指南](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md)，详细兼容性和API以[支持矩阵](format/83-release-support-matrix.md)为准。
- 当前源码与功能以当前分支每次最新提交（`HEAD`）为准，不锁定到版本标签或指定提交。
- 当前工作只在[TODO](TODO.md)记录，开发和文档规则见[CONVENTIONS](CONVENTIONS.md)。不保留发展路径、历史决议、施工计划或已完成任务流水。
- 已有版本标签仅作为历史发布标识；构建和后续开发默认跟随最新提交。GitHub Release由用户手动创建，不推断线上发布状态。

## 输入与支持范围

详细类型、键、布局、版本组合、API和预算以[第83章支持矩阵](format/83-release-support-matrix.md)为唯一正文。本文件只保留判断输入和请求所需的概要：

| 维度 | 当前范围与入口 |
|---|---|
| MySQL 8.0 | 明确验收8.0.11/12/16/19/23/28/30/45；自动SDI发现，准确组合及旧式/现代INSTANT见[第93章](format/93-mysql80-patch-compatibility.md)；8.0.29明确排除，依据见第93章 |
| MySQL 5.7 | 明确验收5.7.9/10/11/12/28/43/44；使用可信Schema/SecondarySchema，实例驱动采集和版本/连接限制见[第92章](format/92-mysql57-patch-compatibility.md) |
| 物理快照 | 16 KiB、独立非压缩非加密表空间、crc32；目标写事务已结束，整个读取期间文件与可信定义保持稳定 |
| 行与列 | DYNAMIC/COMPACT及已验收DML/DDL组合；既有数值、文本/二进制、时间、ENUM/SET、JSON、二维空间值及SQL NULL；精度/宽度以具体版本证据为限 |
| 文本与索引 | 已验收字符集及四个 `_bin` 字符键规则；多层聚簇、复合键、实际唯一非空键、隐藏DB_ROW_ID和普通BTREE二级；5.7不推定8.0物理DESC/INSTANT/INVISIBLE |
| 生成列 | STORED物化；VIRTUAL只在显式仅物化入口单列报告，不求值或填伪NULL；8.0不可见列按版本组合验收 |
| 局部库接口 | 原始/校验页、可信Schema的聚簇/二级本地页；未解引用列明确返回ExternalField，不认证整树、分配或事务；见[第95章](format/95-local-page-reading.md) |
| 字段物理来源 | 显式Trace入口，返回本地载荷、NULL位/长度项、INSTANT默认和DROP/系统字段来源；不隐式读取LOB；见[第96章](format/96-field-provenance.md) |
| 聚簇列投影 | 独立扫描/查询入口按请求列序返回投影记录，只物化所选LOB；保留页内及导航校验，见[第97章](format/97-clustered-projection.md) |
| 结构化诊断 | Problem代码/来源及有界Diagnose报告，区分执行完整、通过、失败与无法验证；见[第98章](format/98-structured-diagnostics.md) |
| 行列存储分析 | 当前列载荷、逻辑长度、公共记录开销、INSTANT默认及去重LOB引用页容量；见[第99章](format/99-row-column-storage.md) |
| 索引交叉校验 | 可信导出证据绑定输入后双向核对所选二级与聚簇行，区分矛盾及无法认证；见[第100章](format/100-index-consistency.md) |
| 完整分区集合 | 普通RANGE/LIST/HASH/KEY；8.0通过SDI/manifest v1，5.7通过可信PartitionSchema/manifest v2；定义序扫描，DDL及方法边界见[第94章](format/94-partition-version-compatibility.md) |
| 独立CLI应用 | metadata/page/export/query/check/space/stats/visualize；JSONL/CSV带类型无损导出，stats输出精确JSON/表格，HTML按请求层分别认证 |
| 输入准备 | schema通过Go MySQL驱动采集已验收5.7单表或完整分区目录，支持TCP/socket及证书验证TLS；manifest准备完整清单。仅采元数据，物理快照由外部备份工具提供 |

「支持」不意味着这些维度的任意组合均经过实测；完整类型物化单值上限仍为16 MiB。元数据预检不认证全部列值或同一事务时点，可信定义的正确性和稳定快照由调用方保证。

## 功能与技术入口

| 功能 | 当前说明 |
|---|---|
| SDI与表级动态元数据 | [第43章](format/43-sdi-schema.md) |
| 六视图统计与精确JSON | [85–86章](format/85-statistics-model-and-metrics.md) |
| 5.7可信Schema与二级读取 | [87–88章](format/87-mysql57-explicit-schema.md) |
| 系统表空间知识 | [89–90章](format/89-mysql57-system-tablespace.md)，不增加系统空间解析或恢复能力 |
| 驱动采集与清单生成 | [第91章](format/91-cli-input-preparation.md) |
| 明确5.7补丁/连接差异 | [第92章](format/92-mysql57-patch-compatibility.md) |
| 明确8.0补丁/INSTANT/LOB | [第93章](format/93-mysql80-patch-compatibility.md) |
| 两分支完整分区集合 | [第94章](format/94-partition-version-compatibility.md) |
| 独立页与本地记录读取 | [第95章](format/95-local-page-reading.md) |
| 字段级物理来源追踪 | [第96章](format/96-field-provenance.md) |
| 聚簇列投影及按需LOB | [第97章](format/97-clustered-projection.md) |
| 结构化校验与诊断 | [第98章](format/98-structured-diagnostics.md) |
| 行列存储分析与精确归属 | [第99章](format/99-row-column-storage.md) |
| 聚簇与二级索引交叉校验 | [第100章](format/100-index-consistency.md) |

## 基础库能力

待解决的问题：上层应用需要可局部读取、逐字段解释、按需物化及校验的基础库能力，避免重复物理解码或依赖整表结果。

当前提供下述独立页、记录解析、字段级来源、聚簇列投影、结构化诊断、行列存储分析与聚簇/二级索引交叉校验入口，工作安排见[当前任务](TODO.md)。当前已验收支持仍以支持矩阵为准。

选用方案是复用现有解码器、扫描/查询、LOB读取及带类型值模型，优先使用标准库，将结构化结果和校验范围提供给CLI、可视化等上层。首轮限已有格式范围内的单表，不重写解析器、不引入新依赖或通用SQL执行框架，也不新增分区集合查询。

基础库与CLI按[项目边界](LIBRARY_ASSESSMENT.md)拆分为两个独立Go模块。库模块为`github.com/kimihiro-dev/innodb-go-reader`，保留根包`innodb`、`rowio/stats`及库示例；同级CLI项目模块为`github.com/kimihiro-dev/innodb-go-reader-cli`，迁入命令入口、CLI编排、MySQL采集及内部HTML实现。旧本地模块名和公开visual包不保留转发层，迁移说明随文档交付。

问题：调用方需要独立依赖离线基础库，上层应用需要独立开发、验证和发布。选用Go内置模块及工作区、现有公开API和固定测试子集，不引入新依赖，不同时重组解析子包或重设计API。单仓单模块不能隔离发布与模块依赖，将rowio/stats一并迁出会增加可复用公开协议的迁移范围，因此不采用。

验收：库只依赖标准库，原始夹具/证据和核心解码实现不变；应用固定输入按源路径及SHA256验证，保留现有命令、格式、错误/预算及输出保护行为；两端适用测试、race/vet、文档和独立消费者通过。联合开发工作区置CLI目录并忽略提交，避免影响父目录其他项目。CLI使用可远程取得的真实库版本并在关闭工作区时验证；本地工作区只用于联调。提交和正常推送按用户授权执行，GitHub Release仍由用户手动创建。

各项按TODO中的依赖和可测标准实施；具体公共契约在对应任务开始时写入本文件，解决开放问题后再编码。局部解析、列投影和诊断不得冒充完整当前行或事务一致性；新功能必须保留损坏、不支持、未请求、未验证及SQL NULL的区别。格式与恢复相关非目标不变。

### 独立页与记录解析契约

问题：上层需要读取指定页及其本地记录，不应依赖整树扫描、SDI自动发现或全文件分配分析。

选用最小方案：复用标准库`io.ReaderAt`、`encoding/binary`及现有`readPage/parseIndex/decodePage/decodeSecondary`，新增四个公共入口，不引入依赖。`ReadRawPage`返回独立16KiB原始字节，不作内容校验；`ReadPage`严格检查CRC32C、头尾LSN低32位与请求页号，返回`PhysicalPage`的通用`PageHeader`和原始字节，不解释未知页类型的内部结构。按ReaderAt契约，完整页伴随`io.EOF`允许成功，其他底层错误保留。

`ReadClusteredPage`与`ReadSecondaryPage`接收可信Schema/SecondarySchema，读取页0认证既有表空间标志及空间身份，再只读取指定INDEX页。复用局部目录、记录链、边界和键顺序检查；节点检查子页号非零、非自身且在文件内，但不跟随子页或兄弟页，不读取SDI或指定Schema的根页，除非根页就是目标页。不增加任意偏移的孤立记录解码，不把整树读取伪装成局部接口。

聚簇叶页返回独立定义类型`LocalRecord`，保留既有Record来源字段；其Values内普通值沿用既有类型，`nil`仅表示SQL NULL，未解引用列使用`ExternalField`值而不是伪NULL。CHAR普通值按既有规则处理且保留物理文本。删除标记只返回既有DeletedRecord证据，非叶页只返回NodePointer。VIRTUAL定义可存在但不求值，列索引始终指向Schema.Columns。二级页沿用SecondaryRecord/SecondaryNode，局部检查有效非NULL唯一键，不认证跨页唯一性或聚簇对应关系。

原始与校验页入口只认证指定16KiB范围，无法独立证明所属表空间格式或分配；局部索引入口还依赖页0与可信定义，但不认证目标页当前分配、父子范围、兄弟互链、整树完整性、页外值或事务可见性。所有接口失败返回nil及可分类错误，无部分成功；同步单页接口不新增context或预算配置，每次最多读取页0与目标页，结果字节和值由调用方持有。

验收：以固定真实夹具及其独立SQL/官方预期和人工字节为依据，覆盖聚簇/二级、叶子/非叶子、DYNAMIC/COMPACT、代表INSTANT及删除标记。I/O跟踪证明只访问契约指定页，无关树/SDI/LOB损坏不妨碍局部解析；目标页或必需页0损坏、短读、错误身份、越界及记录链损坏明确失败。新结果与已有完整读取交叉比较仅作为回归，不能替代独立预期。现有CLI的page命令保持原空间分析语义。

### 字段级物理来源契约

问题：调用方需要将列值对应到真实载荷、NULL位、长度项及INSTANT默认来源，不能从显示值或记录总范围猜测字段偏移。

选用方案：新增`TraceClusteredPage/TraceSecondaryPage`，复用已有局部读取及解码器，在解码时显式收集来源；普通Read/Scan/Query和局部Read入口不启用追踪。标准库`io.ReaderAt`与`encoding/binary`解决字节访问，已有依赖没有InnoDB字段来源模型；不新增依赖、不另写解码器，不为普通读取永久分配逐字段来源，也不通过重新编码值推算位置。

追踪返回既有ClusteredPage/SecondaryPage及可省略的Sources集合，每个RecordSource以记录origin关联现有行、节点或删除证据，标明Node/DeleteMarked及实际存在的行版本/旧式字段计数区间。FieldSource报告逻辑Column、Name、实际记录中包含系统字段的Position、Kind、Null、本地Data、逆序Length项、NullBit、Prefix、Reference及可信Schema中的Default。Kind区分stored/default/dropped/system；Column为-1的系统字段与DROP字段按Kind区分，默认字段Position为-1。二级Position对应物理字段序，Column对应SecondaryField.Column，同一列的前缀和完整定位副本分别报告；非叶页只报告实际键和子页号。

ByteSpan的Start/End为页内左闭右开区间；文件偏移为页号乘PageSize再加页内偏移，记录相对偏移为页内偏移减origin。存储的空值保留零长Data区间，SQL NULL无Data且Null为true，可空列保留NULL位字节和掩码；缺失或固定宽度字段不伪造Length。INSTANT默认来源复制Schema的编码字节及NULL标记，无本地字节区间；现代DROP仅保留实际存在的旧物理字段位置和元信息，不还原历史值。VIRTUAL只在追踪页的独立VirtualColumns中报告，无字段载荷。来源由返回结果持有，修改默认字节不改变输入Schema。

LOB追踪只标识本地前缀与20字节引用，保持页0与目标页的I/O范围，不自动读取链。外部Data包含前缀和引用，Prefix/Reference是其子区间，不可重复计量；引用通过既有LocalRecord.External与Column关联。已物化值的页外来源仍以Record.External.Chunks为准，显式StreamLOB时以LOBBlock.Chunk/Offset/Prefix及Complete为准，不伪造未读取的块，也不新增MVCC或历史链还原。删除记录的来源只解释布局，不将未解码的历史非键字段当作当前SQL值。

追踪失败返回nil，保留既有错误分类和I/O原因；删除/DROP残留中带外部标记的载荷也须至少20字节，否则无法给出合法引用区间并以ErrCorrupt拒绝。

验收：固定样本逐字节核对NULL/空值、多字节文本、定长/变长一两字节长度、复合/隐藏键、COMPACT前缀和二级前缀；旧式/现代INSTANT的真实混合行版本正确关联当前列、默认来源及DROP残留。所有区间落在所属页和记录边界内，NULL/长度项互相独立且不冒充载荷；系统字段不变成用户列。追踪与普通局部读取的列值、节点、删除证据一致；独立SQL/人工字节与既有接口互比分别标识。I/O、损坏、Schema错误、默认字节所有权及实际LOB块拼接有验证，原有读取/导出语义不变。

### 聚簇列投影契约

问题：聚簇扫描、点查和范围查询只需要少数列时，现有完整行入口仍物化所有LOB，导致不必要的I/O或因未请求值损坏/超限而失败。

选用最小方案：新增`ScanProjected`与`QueryProjected`及各自的Auto、Materialized、MaterializedAuto变体；扫描接收`columns []string`，查询接收`ClusteredQuery{Range KeyRange, Columns []string}`。标准库`io.ReaderAt`、context及现有scanReader/LRU已提供访问和控制；既有依赖无InnoDB投影能力。复用二级查询的列选择校验、既有树遍历及延迟LOB引用，不新增依赖。拒绝对完整Read结果事后裁列（仍会读取未选LOB）、改写普通Record为不完整行、另建树遍历器或放宽物化上限。

列名严格匹配存储列，nil选择所有存储列；显式空列表、重复、未知及VIRTUAL列拒绝。顺序按请求列表；返回`ProjectedRecord`这一独立定义类型，其Values只包含所选列，`nil`只表示所选列的SQL NULL，未请求列不占位置。沿用Record的页/记录/系统字段及行版本；DefaultColumns、TextBytes、CharStorage、EnumIndexes、SetMasks及External.Column重新映射到投影列序。External只保留已选择并成功物化的引用与Chunks；键仍在内部用于导航和完整性校验，未选择的用户键不自动加入Values。`ProjectedEvent`分开报告Page、Node、Record与DeletedRecord，节点/删除证据不按列投影，不解释为当前行。

扫描返回`ScanReport`，查询返回`QueryReport`，报告Columns为所选定义；默认入口在存在VIRTUAL时拒绝，仅Materialized变体单独报告VirtualColumns且不求值。普通Read/Scan/Query保持完整值语义。仅新增单表库入口及最小示例，不新增CLI参数或分区集合投影。

所有访问页继续校验CRC、身份、记录链、目录、局部全部页内值和必要导航；本地外部引用本身也须有效。选中行只解引用所选LOB，不认证未读取页外链/类型。扫描仍检查完整聚簇树的两端及兄弟链；查询沿用KeyRange、PointKey、Prefix、Reverse与Limit，仅认证访问路径和所请求范围。投影不得绕过页内损坏或改变删除标记处理。

元数据、导航及选中LOB共享一个context、缓存和累计页/遍历/行预算。MaxRowBytes按整条本地记录字节加所选外部后缀和所选INSTANT默认编码计数；前缀已含在本地记录中，不重复计量。MaxRows在LOB读取前检查，16MiB单值类型上限不变。回调可保留/修改结果，后续事件不受影响；错误保留errors.Is分类及底层原因，回调计数包含报错的调用。只有nil error及Complete表示所请求投影范围完成；LimitReached不证明还有更多行，失败前缀不可撤回。

验收：投影与固定独立SQL的相应列一致，覆盖请求重排、NULL/空值、精确类型、复合/隐藏键、两行格式、旧式/现代INSTANT、生成/不可见列及删除标记。I/O记录证明未选LOB不访问；未选页外损坏及超16MiB值不阻碍合法投影，选择同一值明确失败。验证所选来源/辅助字典索引映射、整树两端、范围/反序/Limit、空范围、Schema/列名错误、页内损坏、停止/取消/I/O及共享预算。全量相关回归、示例和文档检查通过，固定夹具及原始证据不变。

### 结构化校验与诊断契约

问题：调用方需要在有界工作量内收集独立损坏并定位原因，不能解析错误字符串或把依赖缺失及诊断完成当作输入通过。

选用最小方案：新增可信Schema的`Diagnose(ctx, reader, size, schema, DiagnosticOptions)`，选项含ScanOptions共享预算、MaxPages（默认1000000）、MaxIssues（默认1000）、可选Secondary定义及SkipLOB。默认检查当前聚簇LOB；SkipLOB时明确标为not_requested。标准库errors.As/Is/Join负责结构化错误及原始原因，io.ReaderAt/context与既有scanReader、空间/记录/树/LOB解析提供实际校验；现有依赖无此诊断模型，不新增依赖。不另写宽松数据恢复器、不重封或绕过坏CRC、不解析字符串推断错误类别、不增加CLI参数或自动SDI诊断。

`Problem`实现error及Unwrap，报告稳定ProblemCode、Category（corrupt/unsupported/io）、Message、Expected/Actual与ProblemLocation。位置仅填写已知页、索引、记录页/origin、逻辑列/名称及页内ByteSpan；字节区间属于Page，LOB错误的记录所属页用RecordPage单列，不能混用。观察字节最多摘要64字节，不复制整个坏值。底层I/O及既有errors.Is分类保留；既有严格入口在有结构化来源时也可errors.As取得Problem，原有失败无部分结果及错误文本保持含义。未逐条件细分的布局/导航错误用对应子系统代码，不承诺每条内部断言各有独立代码。

`DiagnosticReport`只包含问题、检查范围/状态与计数，不返回可导入的行。检查层包含输入、Schema、使用中页封套、局部INDEX/SDI头/链/目录、完整空间分配，以及所选索引的树导航、局部列值和当前聚簇LOB。状态区分passed、failed、unsupported、blocked、incomplete和not_requested；受损依赖导致后续检查blocked，已检查前缀不认证整个范围。事务可见性、可信快照来源、未选索引、VIRTUAL表达式及聚簇/二级相互一致性明确列入未认证范围。

先复用空间描述符解析，只有页0/XDES元数据有效后才依据分配位图检查使用中页；free/uninitialized/file-tail残留不因坏CRC误报损坏。逐页收集独立封套/局部布局问题，每页遇到不可靠布局停止该页依赖检查；描述符损坏不猜测后续分配。复用AnalyzeSpace严格验证分配及段/层级，失败时不返回部分SpaceReport供后续认证；相关依赖被阻断。分配通过后要求所选根为使用中页，再逐所选索引页解码列值，可独立报告不同页的字段问题；某页解码失败不继续猜该页其他字段。局部值校验通过的索引再复用完整树遍历，双向核对访问页与该索引已分配页集合；各索引独立报告，不做交叉校验。LOB逐当前引用检查，坏链不妨碍其他独立引用；保留16MiB类型物化限制。

所有阶段共用context、LRU及累计页请求/遍历/行预算，包含描述符、空间复验、局部解码、树复验和实际LOB访问，不按阶段重置。空间既有步进也纳入同一遍历预算；MaxPages约束文件规模，MaxIssues约束保留问题数，超限立即返回ErrLimit且Truncated=true，不伪装完整报告。当前行按本地字节及需要校验的外部后缀/默认编码执行MaxRowBytes；缓存和预算不承诺RSS硬上限。固定夹具及原始证据保持不变。

检测到损坏/不支持时记录Problem，若其余独立检查及阻断判定已执行完，返回nil error、Complete=true、Passed=false。Passed只表示实际请求且可执行的支持范围检查全部通过，不认证上述未认证范围。I/O可在预算内继续检查独立页/引用，但返回保留原因的错误、Complete=false；取消、停止及预算中断立即停止，未完成检查明确incomplete。问题去重不能丢掉不同页/记录/列的原因；错误链及报告控制结果分别可查询。

完整ReaderAt页伴随io.EOF允许成功，短读及其他I/O原因不吞掉。

验收：正常固定样本在请求范围通过；free页残留不误报；两处独立坏页分别定位，根/描述符/分配结构损坏令依赖blocked。字段和LOB损坏具有适用的页/记录/列/字节来源，INSTANT错误列映射仍指当前Schema。完成但不通过与中断分别验证，问题/页/遍历/行/字节预算及取消/I/O原因保留。原有严格读取、投影、导出和原子发布回归不被削弱；示例、技术范围和文档检查同步完成。

### 行列存储分析契约

问题：需要精确解释当前未删除物理行的列载荷及公共记录开销，现有空间统计无法给出这些归属。

选用最小方案：新增可信单表`AnalyzeStorage(ctx, reader, size, schema, ScanOptions)`及显式允许VIRTUAL定义的`AnalyzeStorageMaterialized`，成功返回StorageReport指针，任何失败返回nil及保留分类/原因的error。复用完整聚簇树扫描、字段来源收集、投影的全列物化及LOB块来源，标准库math/bits、排序和整数聚合即可实现，不新增依赖。拒绝全表Read后统计（保留所有行）、SQL显示值重新编码猜物理长度、逐列重复扫描、可变精度浮点累计及全空间报告冒充列归属。首轮无Auto、列筛选、采样、优化建议、CLI参数或分区集合接口。

每列保留定义及MaxEncodedBytes：变长文本/二进制/LOB为声明编码容量，其他为固定存储宽度，不乘行数冒充已占用空间。Rows、Stored、Defaults、Nulls、DefaultNulls、EmptyValues、ExternalValues分别计数；Stored与Defaults覆盖全部当前物理行，Nulls包含默认NULL，EmptyValues只统计非NULL文本/二进制的逻辑零长度。INSTANT默认没有本地载荷/长度项，默认编码字节单列统计；DROP字段不归到同名新列。VIRTUAL不统计值，只有Materialized入口单列定义。

每列Encoded长度统计覆盖所有非NULL当前值，取完整InnoDB值编码长度（本地前缀加页外后缀、或可信INSTANT默认；不含20字节引用），保留CHAR物理空格、JSON二进制及数值固定编码。Logical长度仅适用于文本/二进制：使用声明字符集的字节长度，CHAR去尾空格、BINARY保留补零；其他类型为nil，不能把显示字符串长度混入编码长度。Local长度只覆盖非NULL且实际存储的字段，包含前缀和引用；LengthBytes、PrefixBytes、ReferenceBytes、ReferencedExternalBytes为明确分开的元数据/子集，不可与Local.Sum重复相加。

长度统计使用uint64的Count/Sum/Min/Max和固定65桶：桶0为0，桶k为[2^(k-1),2^k-1]（k=1..64）；无观测时Count=0且Min/Max=0。所有计数及加法检查溢出，溢出以ErrLimit失败；JSON中的64位计数和桶以十进制字符串输出，不经float64。

行级报告包含当前LocalRecord长度统计，以及HeaderBytes（固定5字节）、NullBitmapBytes（按整条记录保留区计一次）、InstantHeaderBytes（版本/字段计数）、SystemBytes、DroppedPayloadBytes、DroppedLengthBytes。所有列的Local.Sum和LengthBytes，加上述公共项，必须等于LocalRecord.Sum；DROP长度项只归公共项，NULL位不逐列重复计字节。删除标记记录和非叶节点分别计数及本地字节，不计入当前列/行分布。页目录、页头尾、空闲与垃圾不摊到列。

ReferencedExternalBytes按当前引用累计后缀，允许同一物理载荷被多次引用；UniqueExternalBytes按当前Chunks页内区间并集去重。LOBReferencedPages只取当前块的数据页、活动项所在索引页和引用首页，按页号去重；仅一个当前列引用的页计入该列ExclusiveLOBPages，跨列共享页统一计入SharedLOBPages，不重复分配给各列。页容量均为唯一页数乘PageSize，包含同页结构开销/可能的历史残留，不等于当前载荷字节。未引用分配页及仅历史/空闲索引分配链页不归当前列；校验为读取当前链而访问的额外页不自动变成当前占用。

完整聚簇树及全部当前值/LOB均严格校验。树页容量与上述LOB页容量仅说明访问/引用范围，不认证FSP分配、其他索引、MVCC可见性或快照来源，也不估算可归还磁盘空间；这些限制在报告Unverified中声明。局部来源在同一次解码中收集，不重新读取每页或保留每行。树遍历、全列物化和LOB共享context、LRU及累计ScanOptions预算，16MiB单值类型上限不变；聚合内存按列固定桶及有预算约束的唯一LOB页/区间增长。

验收：固定SQL预期独立核对行数、NULL/空文本/二进制和逻辑长度；lesson_rows人工逐字节核对记录总量、列载荷、共享NULL位图和长度项，保持分解恒等式。覆盖COMPACT/DYNAMIC前缀与新/旧LOB、当前更新、隐藏/复合键、INSTANT默认/DROP、生成/不可见列及已支持5.7/8.0代表组合。共享引用去重、整数溢出、桶边界、空表、取消/I/O/CRC/导航/各预算及16MiB边界分别验证；失败不返回部分成功报告，固定夹具及原始证据不变。

### 聚簇与二级索引交叉校验契约

问题：单独读通聚簇树和二级树，不能证明当前有效行与所选二级条目一一对应。

选用最小方案：新增可信单表`CheckIndexConsistency(ctx, reader, size, table, indexes, ConsistencyOptions)`及显式允许未索引VIRTUAL定义的Materialized变体；要求至少一个显式SecondarySchema，不自动发现或承诺索引清单完整。标准库crypto/sha256绑定证据与输入，encoding/binary和map建立有界定位键表；复用全树遍历、投影按需物化、secondaryPrefix、encodeQueryValue、compareSecondary和结构化Problem。已有驱动依赖不提供离线交叉校验，不新增依赖。拒绝整表Read保留行、仅比较条目数或指纹，以及为每行重复完整回表；每个索引依次扫描聚簇树和二级树，只保留该索引所需的预期键/定位键/来源。

首轮快照前提由调用方提供可信ExportSnapshotEvidence：Source为独立采集记录引用，SHA256为采集文件解压后原始字节的摘要，ExportCompleted、CopiedWhileLocked和WritesCompleted均为true，分别声明成功FOR EXPORT、在导出锁持有期间完成复制、目标写事务已结束且无待恢复/准备写事务。采集记录需证明相关change buffer已由成功导出流程合并，delete-mark仅为已完成写入留下的历史证据。库验证声明字段及输入摘要，不读取/解释Source文档，不认证声明真实性或自动推断提交状态；调用方仍保证定义/文件稳定。只提供ReaderAt、普通文件副本、未完成合并/恢复的快照，或证据缺失/摘要不匹配时，Preconditions为blocked、Passed=false，无确定索引损坏发现，不进入索引比较。单文件摘要只用于绑定采集证据与输入。

报告分开Complete、Passed、Truncated、Preconditions、每索引状态和Issues。证据不足但判定完成可Complete=true、Passed=false且error=nil；满足可信前提后发现缺项、多项、值错配、重复定位或唯一性矛盾，同样以结构化Issue报告、Passed=false。每条Issue含目标二级索引身份及Problem，适用时另给Clustered来源；Problem.Location只指真实已知的物理页/索引/记录/列/字段区间，不编造不存在二级项的位置。Expected/Actual字节摘要沿用64字节上限和errors.Is/As原因链。全树损坏/不支持阻断该索引的后续缺项判定，可继续其他所选索引；I/O、取消、停止及预算立即中断，Complete=false并保留原始error，未完成项为incomplete。MaxIssues默认1000，超出保留预算返回ErrLimit并Truncated=true。

聚簇扫描使用当前非delete-mark行，按所选二级字段的原有类型、字符集、前缀、ASC/DESC和NULL规则构造完整物理键，包含去重或重复的完整聚簇定位副本；二级有效项按完整定位键关联聚簇行，再逐物理字段比较。定位键编码逐成员区分NULL/空字节/边界，CHAR/VARCHAR沿用现有_bin PAD SPACE等价规则；隐藏DB_ROW_ID只在同一文件内作为实际定位身份。定位存在而值不符报告mismatch，不额外把同一行报为missing；同定位多有效项报告duplicate_locator，没有当前行报告extra，全部树遍历完成后未被定位的当前行报告missing。唯一非NULL声明键继续使用已有整树唯一性检查，NULL唯一键可重复。删除标记条目分别计数，不用于覆盖当前缺项，不解码/恢复其历史非键值。

只物化所选索引涉及的列（包括定位列）；未选LOB不跟随，页内字段仍严格检查，相关LOB沿用16MiB类型限制。不求值VIRTUAL；Materialized单列定义。每索引复用完整聚簇/二级导航及两端兄弟链校验，不认证FSP分配、未提供定义的其他索引、SQL ReadView或外部来源真实性；报告Unverified明确列出这些范围。

所有索引、文件摘要和重复聚簇扫描共用一个scanReader/context/LRU及累计ScanOptions预算，命中缓存仍计请求；MaxRows包括每次扫描的当前聚簇行和二级有效项，MaxRowBytes按现有投影/二级记录源字节计量。MaxBufferedBytes默认64MiB，限制一个索引保留的定位键编码及预期物理键原始字节，PeakBufferedBytes报告峰值；行数/遍历预算另限制map和来源对象数，不承诺RSS硬上限。报告计数JSON使用十进制字符串，Issues不返回可导入完整行。

验收：原始采集SQL/manifest的摘要和当前行/二级项数独立核对；覆盖NULL、重复值、唯一键、混合方向、字符/二进制前缀、定位去重/重复、隐藏/复合/实际唯一键、两行格式、INSTANT/生成列和已支持5.7/8.0代表组合。只改一次性副本构造删除标记导致缺项、移除历史项的删除标记导致多项、错误定位、同计数值错配及唯一性矛盾；合成副本单列重新绑定测试证据，不冒充原始采集。无证据、待合并/未提交声明和错误摘要不得误报通过或确定损坏。验证问题上限、缓存/累计预算、缓冲字节、I/O/取消/CRC/导航和未选大LOB；固定夹具/原始证据不变，示例/手册和适用回归同步交付。

## 验收标准

1. 全部请求列值与独立SQL/官方工具预期一致，精度、类型、NULL和空值保持区别；新版本/组合单独取得证据。
2. 物理偏移、记录/树/LOB来源可逐字节复核，固定夹具及原始证据不改写。
3. 可检测损坏、不支持或超限明确报错；完整结果失败无部分成功，流式输出核对error与Complete，文件仅完整成功后原子发布。
4. 当前范围、非目标、完成与预算契约可查；格式解释和可复制使用示例随能力交付，stdout数据与stderr诊断分离。
5. 实现运行适用回归；历史通过不算本轮重跑，源码事实、真实预期和合成测试分别标识。首版复验见[第84章](format/84-release-validation.md)，对应技术证据从[验证资料](README.md#验证资料)进入。

## 明确非目标

- 不外推未验收补丁、升级混合文件、任意类型/精度/DDL组合或其他MySQL/MariaDB版本；8.0.29继续排除后续兼容与验收。
- 不提供其他页大小、系统/通用表空间、压缩/加密或REDUNDANT支持；不将知识文档视为解析能力。
- 离线命令不隐式连接实例或采集在线快照；schema仅采元数据，不提供redo/undo回放、MVCC可见性、事务恢复或删除历史值还原。
- 当前不提供双快照比较或数据指纹功能。
- 不实现通用SQL/生成表达式/空间拓扑引擎或FULLTEXT/SPATIAL/函数/多值索引；分区不增加子分区、LINEAR/COLUMNS、未验收KEY算法、集合范围查询、路由或全局归并。
- 不因版本标签增加永久API兼容承诺；发布按用户授权执行。

## 文档维护与发布

当前需求、约定、任务、CLI和支持矩阵共同说明项目状态；新增实现直接更新相关正文，不追加决议或过程记录。技术解释、固定夹具、测试和独立报告保留，用于验证实现行为，资料导航见[文档目录](README.md)。

[构建指引](RELEASING.md)默认导出最新提交并用SHA标识产物；已有[发布说明](releases/v0.1.0.md)仅描述其对应历史版本，不作为当前开发基准。
