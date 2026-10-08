# 历史决议记录

**性质：2026-09 至 2026-10 的施工记录，不是现行规范。**

保留当时的问题、输入范围、备选方案、取舍和澄清过程；早期限制可能被后续阶段替代。现行选择见[决议](../DECISIONS.md)，当前支持范围见[支持矩阵](../format/83-release-support-matrix.md)。验收正文集中保存于[历史验收记录](../HISTORY.md)，原始日志不改写。

## 2026-09-09：先调研，再确定实现范围

- 依据：用户要求先理清参考项目功能，并指出最终功能可能不同。
- 决定：本轮交付源码分析和阶段建议，仅建立文档；Go 是用户指定语言，框架及第三方依赖尚未选择。
- 分析基线：[InnoDB Java Reader 1.0.10](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a)，调研时使用本地快照；源码导航现固定至该版本提交，不将上游当前分支等同调研版本。
- 未采用：直接完整移植 Java 类结构。原因：范围未定，兼容性限制需先核实。
- 未采用：直接从完整 SQL 查询能力开始。原因：页、记录、元数据与比较语义需要分别验证。
- 测试实例本轮仅执行只读元数据查询；后续测试数据与物理快照方案待确定。

## 2026-09-09：数据还原优先，文档与实现同步

- 依据：用户明确要求先完成核心文件数据解析，能够完整解析存储的数据；每步实现同时形成详细讲解文档，支持跟随学习。
- 决定：后续阶段以数据还原能力作为主要验收目标，数据页统计、分析与可视化建立在解析能力上；每步文档是交付组成部分。
- 原报告的“先做独立页检查器”仅为待确认建议，本次用数据还原路线替代，没有覆盖已批准的实现决策。
- 未采用：页统计完成即视为首阶段完成。原因：未验证记录布局与用户数据解码，不满足用户优先级。
- 未采用：一次性覆盖全部版本、类型与事务恢复。原因：无法形成适合逐步学习的可验证增量。
- 首阶段具体建议见 [第一阶段计划](../STAGE_PLANS.md#stage-1)；本轮只规划，不开始实现。建议中的输入约束、显式 schema 和根页输入仍待确认。

## 2026-09-09：批准并实施第一阶段

- 用户已同意按计划实现；接受计划中的单聚簇叶子页、8.0.45/16 KiB/DYNAMIC、显式 schema 与根页元数据限制。
- 使用 Go 标准库，模块名暂定 `innodb-go-reader`，根包 `innodb`。读取接口接收 `io.ReaderAt`、文件长度及显式元数据，调用方负责关闭文件。
- 分为 schema、page、record 三个小文件；返回列值及记录头/事务字段原始信息。错误用 `%w` 和明确上下文传播，提供不支持与损坏两类哨兵错误。
- 不实现完整 CLI；提供可执行示例帮助复现。采用离线夹具测试和有界字节读取，不引入 Java 风格工厂或存储插件。
- 夹具生成使用 Python 标准库驱动现有 mysql 客户端；新建唯一命名的专用数据库，不覆盖既有表。同一客户端会话持有 FOR EXPORT 锁直到复制完毕。密码只经环境传入，不保存。
- 显式 schema 是可信输入契约，不能在未解析 SDI 时证明任意外部 schema 匹配；通过受控夹具、表空间/index ID 和可检测布局进行检查，不声称自动检测全部不兼容表。

## 2026-09-09：批准第二阶段整树扫描

- 用户已批准多页、多层聚簇索引扫描。范围与验收见 [第二阶段计划](../STAGE_PLANS.md#stage-2)。
- 使用标准库 ReaderAt 和显式 DFS 栈，复用叶子解码；Read.Page 保留根页，新增 Pages/Nodes 与 Record.PageNumber 表达多页结果。
- 新夹具 gzip 保存以控制体积，解压由调用方完成；原夹具保持不变。
- 页分裂可能留下 free/garbage 记录，需区分活动记录链与物理堆；不把“只插入”误等同“所有页都无垃圾”。支持合法分裂残留，不读取垃圾记录为用户数据。

## 2026-09-09：批准第三阶段整数扩展

- 范围及验收见 [第三阶段计划](../STAGE_PLANS.md#stage-3)；用户批准上一轮建议。
- 继续仅使用标准库；增加 unsigned 元数据，保留 INT 的 int32 返回值，其他类型按宽度返回原生整数。
- NodePointer.Key 使用 any 与列值一致；内部 uint64 排序值及可选上界避免最大值加一溢出。
- JSON 输出精确整数数字，消费端须保留整数精度。未选择统一 int64、大整数或字符串输出，理由见计划。

## 2026-09-10：第四阶段页内变长字段

- 用户授权规划并执行下一阶段，具体范围与验收见 [第四阶段计划](../STAGE_PLANS.md#stage-4)。
- 放宽 utf8mb4 VARCHAR 声明长度，新增 VARBINARY/max_bytes；仍只解码页内完整值，external 明确报不支持。
- VARBINARY 返回独立 []byte，空切片不为 nil，JSON 沿用标准库 Base64；SQL 预期用 HEX 独立对照。
- 沿用标准库和现有记录解码，不新增类型框架；页外 LOB 单列下一阶段。

## 2026-09-10：变长记录复用后允许 free 为空而 garbage 非零

- 真实 variable_tree 页 8 的 PAGE_FREE=0、PAGE_GARBAGE=2814；官方 innochecksum 通过。
- MySQL 8.0.45 page0cur.cc:1276–1303 复用较大的 free 记录，page0page.ic:786–811 摘除链头但仅减去新记录实际长度，所以可遗留不属于 free 记录的碎片。
- 修正原双向清零检查，保留 free 非零时 garbage 必须非零；同时用活动记录总长度 = heap_top−120−garbage 校验（page0page.ic:770–778），避免单纯放宽校验。

## 2026-09-10：第五阶段初始版本非压缩 LOB

- 用户授权继续下一阶段；[第五阶段计划](../STAGE_PLANS.md#stage-5) 实现第四阶段保留的页外任务，第四阶段“拒绝 external”是当时边界，现有资产仍保留。
- 先覆盖已有 VARCHAR/VARBINARY 的初始版本非压缩 LOB，首个 LOB 页内索引项；暂不扩展类型、压缩、旧格式和历史版本。
- 解析物理记录时保留页外引用，Read 再按索引链还原并校验完整值；引用与数据块出处随 Record 返回，Pages 仍为聚簇页。
- 标准库足够；限制长度和遍历，避免信任损坏引用造成无界分配。

## 2026-09-10：阶段计划统一维护

- 用户要求将前五个阶段计划合并，并将后续阶段追加到同一文档。
- 使用 STAGE_PLANS.md 按阶段顺序保存完整计划，提供目录与稳定阶段锚点；保留当时的范围、备选方案、验收标准和完成说明。
- 更新项目内引用后移除原五份独立计划，后续不再新建分阶段计划文件。当前状态仍由 REQUIREMENTS/TODO 表达，历史阶段边界不改写为当前能力。

## 2026-09-10：第六阶段 TEXT/BLOB 与多页 LOB 索引

- 用户授权下一阶段，范围与验收追加至 STAGE_PLANS.md#stage-6。
- 支持八种 TEXT/BLOB 类型，类型上限用 uint64；实际单值暂限 16 MiB，LONG 超限明确拒绝，保留物化 API。
- 所有 TEXT/BLOB 按 DATA_BLOB 解释长度位；LOB_INDEX 页归属由首个 LOB 页的分配链校验，活动链决定值顺序，Chunks 新增 IndexPage。
- 不新增依赖，不扩大至压缩/历史版本/旧格式；类型和索引格式与官方 8.0.45 源码核对。

## 2026-09-10：第七阶段 DECIMAL 精确小数

- 用户授权规划并实施，范围追加至 STAGE_PLANS.md#stage-7。
- precision/scale 作为显式 schema；DECIMAL 输出保留 scale 的字符串，避免浮点和 JSON 消费端精度丢失；支持 unsigned，主键继续限整数。
- 标准库分组整数和字符串足够，不新增依赖或小数算术 API。类型自带定长，无变长长度项；NULL 不占值字节。

## 2026-09-11：第八阶段 FLOAT / DOUBLE

- 用户授权下一阶段，计划追加到 STAGE_PLANS.md#stage-8；支持普通浮点列和 unsigned，主键仍限整数。
- FLOAT 返回 float32，DOUBLE 返回 float64，按小端 IEEE 754 位模式恢复并保留零符号；有限值才属于当前契约，非有限编码及非零负 unsigned 报 ErrCorrupt。
- 使用标准库，无新依赖；schema 不复用 DECIMAL precision/scale 表示浮点声明，SQL 别名/旧 M,D 暂不处理。SQL JSON 和原始位分别验证，不能以显示小数位数判断是否正确还原。

## 2026-09-11：第九阶段 DATE / YEAR

- 用户授权继续阶段实施，范围追加至 STAGE_PLANS.md#stage-9。
- DATE 返回保留原始分量的字符串，不使用 time.Time 归一化，不拒绝分量范围内的零日期/非日历日期；YEAR 返回 uint16，0 与 NULL 分开。
- DATE/YEAR 均为定长普通列，schema 不允许 unsigned 或其他类型参数，主键仍限整数。
- 标准库足够；特殊日期样本使用专用连接会话级 ALLOW_INVALID_DATES，记录模式，不改变实例全局设置。

## 2026-09-11：第十阶段 DATETIME 与小数秒

- 用户授权继续，范围追加至 STAGE_PLANS.md#stage-10；只实现当前版本 DATETIME2 物理布局，SQL/schema 类型名 DATETIME。
- 新增 fsp=0..6，与 DECIMAL precision/scale 分离；固定宽度解码，输出保留小数位的无时区字符串，不归一化日期。
- 沿用标准库、显式 schema、整数主键；小数秒使用整数解析，校验容量和声明精度对齐。特殊日期仅在生成会话开启 ALLOW_INVALID_DATES 并记录，不改全局配置。

## 2026-09-11：第十一阶段 TIME

- 用户授权规划并执行，计划追加至 stage-11。TIME2 使用 fsp=0..6，字符串输出保留精度，支持负时长和超过 24 小时，不解释为时间点。
- 按官方负值小数借位规则解码，范围限制为 ±838:59:59，端点不能含非零小数；零统一不带负号。
- 标准库足够，独立 TIME 解码，不重构已有类型框架；仅专用测试会话设置并记录 STRICT_TRANS_TABLES，使舍入行为可复现，不改全局配置。

## 2026-09-11：第十二阶段 TIMESTAMP

- 用户授权继续阶段实施，范围追加 stage-12。TIMESTAMP2 返回保留 fsp 的 UTC 字符串，不依赖机器时区，不猜测原会话设置。
- 零秒/零小数返回 MySQL 零日期时间字符串，与 NULL 和 Unix 纪元分开；当前范围为秒数 1..2147483647 及对齐的小数。零秒非零小数拒绝。
- 使用标准库 time.Unix.UTC，仅对正常时间点转换。真实夹具按会话设置构造三时区证据，主 SQL 预期统一 +00:00，不修改全局配置。

## 2026-09-14：第十三阶段 BIT

- 用户授权继续；范围与验收见 stage-13。BIT(1..64) 用独立 bit_length，值统一 uint64，NULL 独立；不将 BIT(1) 特判 bool。
- 固定大端 ceil(M/8) 字节，检查未使用高位；标准库足够，不添加依赖。bit_length 不复用 DECIMAL 或字符串长度参数，主键仍限整数。
- 全位宽与逐位真实样本采用 SQL CAST AS UNSIGNED 和精确 JSON 数字对照；仅新增独立测试库，不改既有数据。

## 2026-09-14：BIT 验证实例与实测布局

- 原测试 socket 不存在，使用相同 MySQL 8.0.45 程序在 /tmp/innodb-bit-stage13 初始化临时实例，仅启用本地 socket、关闭网络及 MySQL X；不启动或修改原实例。验证完成后关闭临时服务，夹具保存在项目内。
- 全位宽表的 69 行真实形成 level=1、两叶子树，生成器按此验证；不人为减少样本以保持单页。首次布局探测库及快照只留在临时目录，最终资产来自新独立库。

## 2026-09-14：第十四阶段 BINARY

- 用户授权继续，范围见 stage-14。BINARY(1..255) 以 max_bytes 指定固定宽度，返回独立 []byte，JSON Base64；完整保留补零，NULL 独立。
- 复用二进制复制，不参与 variableMaxBytes/变长长度读取，不新增依赖或参数。BINARY(0)、CHAR BINARY、二进制主键不在本阶段。
- 原实例未运行，启动上一阶段专用临时实例，仅生成新的独立库，验证后关闭。全长度与混合树样本 SQL HEX 对照。

## 2026-09-14：第十五阶段 ENUM

- 用户授权下一阶段，并告知原测试实例已启动；只读核对 8.0.45/16 KiB 后使用该实例生成新独立库，不停止服务。
- 按有序 enum_values 字典解码，Values 保持字符串/NULL；EnumIndexes 以逻辑列下标保存非 NULL 原始序号，区分零号错误值与合法空标签。
- 继续标准库、可信 schema、整数主键；不实现 ENUM 字典 SQL 解析、排序规则或 SET。字典须为实际 DDL 规范化后的 UTF-8 标签。
- 只对生成会话清空 sql_mode 以产生零号，保存模式与 SQL；不改变实例全局配置。范围/备选方案/验收见 stage-15。

## 2026-09-14：第十六阶段 SET

- 用户授权继续，范围与备选方案见 stage-16。set_values 独立于 enum_values，Values 保持字符串/NULL，SetMasks 保存完整非 NULL uint64 掩码。
- 按 1/2/3/4/8 字节大端解码，33 项起八字节；空标签显示遵循 Field_set::val_str，原始位不丢失。
- 标准库足够，原实例新独立库验证，只有生成会话设置 STRICT_TRANS_TABLES；实例和既有表保持运行不改动。

## 2026-09-14：第十七阶段 CHAR

- 用户授权下一阶段，范围见 stage-17。utf8mb4 CHAR(1..255) 按变长记录读取，max_chars 指定 N，字节宽度 N..4N；复用现有 LOB，在完整恢复后校验。
- Values 返回通常 SQL 去尾 U+0020 字符串；CharStorage 保存非 NULL 的物理存储文本，不混淆显示裁剪与 InnoDB 字节裁剪。
- 标准库足够，原实例新独立库验证，不扩展其他字符集/SQL 模式 API/字符主键。

## 2026-09-14：预先列出完整后续路线图

- 用户要求先查看全部后续待执行功能，本轮只维护规划文档，不启动第十八阶段实现或数据库操作。
- 在 STAGE_PLANS.md 保留已完成 1–17 阶段，追加 18–40 阶段建议和可选扩展清单；标明依赖、验收、学习内容和未决范围。编号用于审阅与跟踪，允许实施前拆分调整。
- 继续数据还原优先，将自动元数据、主键/记录布局、DML/DDL 历史适配列入核心路线，查询/页分析随后；事务恢复与全版本兼容单列可选专题。
- 未采用继续逐轮只公布一个阶段：无法让用户评估总范围。未采用把所有候选特性直接改为已批准需求：用户本轮要求审阅功能，尚未选择所有扩展。
- 简单方案是沿用合并计划与显式支持矩阵，无需新规划文件、依赖或代码框架。未来进入各阶段时再核对官方对应版本源码、细化契约并记录实现决策。

## 2026-09-15：第十八阶段读取时严格 CRC32C 校验

- 用户授权按路线图继续，实施18，其他候选阶段不启动。保持16 KiB非压缩非加密输入，标准库 hash/crc32.Castagnoli 足够，无依赖。
- readPage 对每个实际读取页验证 CRC32C([4,26)) XOR CRC32C([38,16376))，头尾校验字段均须匹配，并比较头尾 LSN 低32位。错误包含页号和文件偏移。
- 严格采用当前 crc32 格式，不自动回退旧算法或跳过校验；页面没有可靠算法标签，失配报 ErrCorrupt 并说明 CRC32C 契约，不能从失配断言使用了哪个旧算法。全零页不能作为已引用数据页返回。未访问的空闲/其他页不扫描，不宣称整文件校验或事务一致性验证。
- 不新增开关或更改 Read 返回类型；仅测试副本可重新封装校验和，保留原有结构损坏测试和 fuzz 深度。校验测试不重写损坏副本。原始资产不修改。
- 验收：全部真实行回归，原始页常量和独立逐位 CRC 对照，头/正文/尾损坏和 LSN 错误，LOB/非根叶子损坏无部分返回，官方 innochecksum 对照，详细手册。无需修改或重启 MySQL。

## 2026-09-15：第十九阶段 SDI 提取与本地源码基线

- 用户提供 /Users/kimihiro/workspace/codespace/cpp/mysql-8.0.45，后续优先只读使用此官方源码。已核对 utilities/ibd2sdi.cc、dict0sdi.h、fsp0fsp.ic；也直接核对 checksum.cc:62–92、245–263，与第十八阶段实现一致。
- 新增独立 ReadSDI(io.ReaderAt,size) API，无 schema；只提取有序(type,id)、原始JSON及页/记录/页外来源，不生成用户表 schema（阶段20）。仍限16KiB、非压缩非加密独立DYNAMIC表空间、CRC32C、可靠离线快照。
- 页0 SDI头位于10505（150+40×256+115），验证SDI标志和物理版本1，从头部读取根页，不写死3。记录固定联合键12字节，叶子系统字段13字节、原始/压缩长度各4，后接压缩数据；页外走SDI_BLOB链，非用户LOB索引。
- 普通未删除SDI记录为本阶段契约；delete-mark/历史布局明确拒绝。非叶子导航和跨页树验证实现，真实既有夹具覆盖多记录与29块页外链，多层导航另用明确标记的合成结构测试。
- 使用标准库compress/zlib、encoding/json、io，无依赖。压缩/解压单对象各限16MiB，累计解压64MiB、最多4096对象；解压须长度精确、消费完整流且JSON对象有效。失败无部分结果，限制为实现保护，不是MySQL格式极限。
- 复用页头/记录链结构验证，通过小型内部回调接入SDI固定布局；不将联合键伪装成整数或提前实现通用复合键框架。不只沿叶子链忽略父节点验证。
- 验收：从全部158个既有资产生成官方ibd2sdi独立预期；离线逐对象对照、页外链来源/长度、根页搬移、多层合成树、CRC/结构/引用/zlib/JSON/资源限制损坏测试、race/vet/fuzz与手册41–42。无需连接或改动实例。

## 2026-09-15：第二十阶段 SDI 到 schema 与索引入口

- 用户授权按计划继续20。新增 InspectTable(r,size) 返回表身份、列/索引元数据、根页验证状态、Issues 和可用 Schema；有不支持特性时保留报告但 Schema=nil。损坏或缺失关键元数据返回错误、无部分报告。新增 ReadAuto(r,size) 仅在报告可读时调用原 Read，错误无部分行。
- 仅匹配当前已验证 mysqld80045/dd80023/sdi80019、InnoDB私有DYNAMIC、单列升序整数主键；未知类型/字符集/生成或不可见列/INSTANT私有属性/分区/二级索引等列为 Issues，不静默跳过。文本自动映射先限已验证 collation255（utf8mb4_0900_ai_ci），binary63；其他排序规则不猜测。
- 元数据映射使用DD数值类型、char_length字节长度、precision/scale/fsp、Base64字典；不解析column_type_utf8/DDL。标准库encoding/json/base64、strconv、strings足够，无新依赖。
- 根页来自各索引se_private_data的id/root/space_id/table_id，与Tablespace对象/FIL页/index ID/level及根兄弟指针核对；不硬编码4或按索引序号推算。聚簇elements中用户键、系统列和普通列的物理顺序必须与当前解码契约一致。
- 解析元数据不证明事务一致性或只插入历史；仍要求受支持稳定快照。表有二级索引时可以报告并核对普通B-tree根，但不扩大现有用户行读取范围。
- 原实例连接受沙箱限制，提权请求被自动审批因检查点兼容性拒绝；不绕过。先完成158离线资产schema/行对照及合成根页错配/搬移测试，并准备新独立库索引新增/删除/重建的快照脚本，真实增删索引验收需恢复审批后执行。
- 拒绝通过正则解析SHOW CREATE TABLE替代SDI、将所有缺失字段默认零、忽略不支持列拼成部分schema、或仅返回无可解释原因的失败。实现与文档43–44同步，实时验证受阻保持TODO未完成。

- 对照日期夹具确认SDI is_unsigned不能不分类型复制：只对整数/FLOAT/DOUBLE/DECIMAL设置用户schema unsigned；DATE/YEAR等不携带此解码属性。读取关键JSON字段采用精确键名，避免encoding/json大小写宽松匹配覆盖规范字段。

## 2026-09-15：第二十阶段真实验收重试

- 用户明确要求重新尝试验收；提权只读连接已恢复。沿用既有五快照方案，新建独立测试库，不改既有用户表或全局配置。
- 真实资产保存到testdata/metadata，生命周期验收通过后纳入默认离线回归；保留SQL、官方SDI及页校验证据，按实际结果更新手册。无需新依赖或新功能框架。

- 验收结果：五快照全部通过，无需修改解析器。原有单主键行读取边界保留；二级root复用5且ID404→405为真实证据，不预设重建必换页号。夹具纳入默认测试，第二十阶段结项。

## 2026-09-16：第二十一阶段 JSON 存储值

- 用户批准继续21，并明确选择类型树：Values中返回JSONValue，另提供普通JSON输出方法。SQL NULL仍为nil，JSON null为非nil类型节点；节点保留二进制类型标签、精确int64/uint64/float64、成员顺序与opaque原始类型/载荷。默认Go JSON编码展示类型树，JSON()方法输出普通JSON视图；普通视图不作为无损类型载体。
- 标准库encoding/binary/json/base64、math足够；没有MySQL二进制JSON解码库依赖。复用既有DECIMAL和LOB读取，不采用encoding/json直接解析存储字节，也不把所有数值转成float64。
- 支持小/大对象与数组、全部标量、嵌套、utf8mb4、DECIMAL/DATE/TIME/DATETIME/TIMESTAMP opaque解释；其他opaque保留类型号及完整载荷，普通视图按MySQL base64:type形式显示，不臆造语义。JSON temporal是八字节小端packed值，不套用普通列编码或UTC秒数。
- 沿用只插入稳定快照；本阶段不实现JSON原地更新留下的空洞和碎片整理。单文档16MiB、最大节点数100000、树深度100为实现保护；显式拒绝超限，损坏偏移/长度/重复键/非法UTF8/非有限double无部分结果。
- 问题：恢复JSON列全部存储值并保留类型和精度。验收：真实SQL语义/JSON_TYPE/精确数值与opaque原始格式对照，小/大容器、页内/页外、混合行/树、空值、资源边界和损坏测试，自动schema/显式schema一致，race/vet/fuzz，手册45–46。

- JSON opaque DECIMAL按my_decimal.h:57–69的9个十进制组容量验证（最多81位，整数/小数分别占组），不误套SQL列DECIMAL(65,30)声明限制；复用解码函数但不改变普通DECIMAL列schema范围。

- json_dom.cc:1501–1505将opaque VAR_STRING(253)按普通文本显示；保留其原始opaque身份与载荷，普通JSON视图按UTF-8字符串输出，其余未知opaque按base64:type显示。

## 2026-09-16：第二十二阶段空间存储值

- 问题：恢复GEOMETRY家族的实际存储值，包括SRID、坐标、集合层次与原始WKB。用户授权按阶段22实施；沿用结构化返回，Values中为GeometryValue，SQL NULL仍nil。保留SRID和独立WKB字节，Geometry树保存类型、每节点字节序、Points/Rings/Geometries。
- 标准库encoding/binary、math足够；go.mod无第三方依赖。复用变长字段及LOB，不引入空间计算框架。拒绝只返回WKT（丢字节证据）、只返回WKB（无法学习结构）和隐式坐标变换。存储坐标保持原顺序，不推断经纬度或转换CRS，不扩展空间索引/地理计算/GeoJSON。
- 支持二维标准WKB的Point、LineString、Polygon、MultiPoint、MultiLineString、MultiPolygon、GeometryCollection和合法空GeometryCollection；解析每节点大小端（真实MySQL存储通常为小端）。EWKB/Z/M等不在范围，非有限坐标、非法计数/子类型、未闭合环、尾随字节明确拒绝，不做拓扑合法性判定。
- schema支持GEOMETRY及七种具体类型；可选SRID指针区分未声明与SRID0，自动入口读取DD geom_type和srs_id_null/srs_id，解码后核对声明。字段仍为最多4字节长度的变长类型，JSON等既有类型不受影响。
- 实现保护：单值16MiB，最多100000几何节点/1000000坐标对，深度100；受控快照、只插入及现有主键/事务限制保持。验收：真实七类型、集合嵌套/空值、SRID0/4326轴序、页内/页外/跨页树，与SQL WKB/SRID/类型/坐标对照，自动/显式schema一致；损坏/资源限制/无部分结果，官方CRC/SDI和race/vet/fuzz；手册47–48。

- 真实8.0.45新建POINT列具有变长长度元数据；不启用旧DATA_POINT固定宽度推断。SRID4326样本存储X/Y为经度/纬度，默认ST_AsBinary按SRS轴序交换，独立对照显式使用axis-order=long-lat并保留默认输出证据；解析器保持原始顺序。阶段22已验收，累计180资产；下一阶段23另行启动。

## 2026-09-16：第二十三阶段字符集与零长度边界

- 问题：按列字符集正确恢复文本、CHAR物理长度与原始字节，避免将所有文本误当utf8mb4。用户授权阶段23；优先utf8mb3、ascii、MySQL latin1并保留utf8mb4，gbk/gb18030等待实际需求。本地源码strings/ctype-latin1.cc明确MySQL latin1为CP1252及五个控制字符兼容映射。
- 标准库unicode/utf8、strings及32项latin1差异映射足够，现有go.mod无依赖；不引入通用转码框架、不使用替换字符掩盖非法序列、不按ISO-8859-1误解latin1。utf8mb3拒绝四字节字符，ascii拒绝高位字节，utf8mb4保持严格UTF-8验证。
- Column.Charset新增可选声明，省略保持utf8mb4兼容；utf8别名归一为utf8mb3。Values继续返回UTF-8 Go string，Record.TextBytes按逻辑列下标保留非NULL CHAR/VARCHAR/TEXT独立完整原始字节；CharStorage保留解码后的含残留空格文本，Values仍裁剪CHAR尾部ASCII空格。ENUM/SET字典按字符集解码为既有标签，原始序号/掩码接口保持。
- 单字节CHAR(N>0)固定N字节、不消费变长长度；UTF8 CHAR按N..N*mbmaxlen变长及既有去空格规则；CHAR(0)/BINARY(0)/VARCHAR(0)/VARBINARY(0)调查并按实际记录长度元数据验收，不把最大长度零与“非变长字段”等同。
- 自动schema先只纳入真实验证的排序规则：utf8mb4_0900_ai_ci(255)、utf8mb4_general_ci(45)、utf8mb4_bin(46)、utf8mb3_general_ci(33)、utf8mb3_bin(83)、ascii_general_ci(11)、ascii_bin(65)、latin1_swedish_ci(8)、latin1_bin(47)。排序规则只用于识别存储编码，不实现文本键比较；其他ID明确Issues。旧255的Charset省略以保持既有schema相等契约，新增UTF8MB4排序规则同样默认；不扩展字符主键。
- 验收：新建独立测试库，四字符集/选定排序规则、全部latin1字节、ASCII字节、utf8mb3边界、定长/变长CHAR、NULL/空/零宽、变长长度255/256、LOB和跨页树，与SQL HEX/CONVERT/CHAR_LENGTH和PAD_CHAR_TO_FULL_LENGTH对照；自动/手工schema一致；损坏、无部分返回、官方CRC/SDI/CLI、race/vet/fuzz，手册49–50。保留16MiB单值及既有快照约束，不修改已有资产。

- 实测确认CHAR(0)/BINARY(0)/VARCHAR(0)/VARBINARY(0)非NULL都消费零长度元数据；单字节CHAR(N>0)无长度字节。采用独立isVariable判定，保留NULL跳过元数据规则。真实latin1全256字节与SQL一致，字典标签按声明编码转换；21快照完成验收，本阶段结项，未启动24。

## 2026-09-17：第二十四阶段复合整数聚簇键

- 问题：按完整有序主键元组恢复聚簇记录和树导航，支持主键顺序不同于列声明顺序。用户授权阶段24，先支持1..16个升序、非NULL、完整整数列，可混合宽度/符号，不新增字符串键、DESC、二级索引或无主键表。
- 保留Schema.PrimaryKey字符串，新增PrimaryKeys []string（JSON primary_keys），两者互斥且必须指定其一；数组允许单元素。自动schema单列仍用历史PrimaryKey，多列使用PrimaryKeys。Values仍按SQL声明列序；NodePointer.Key单列保持原整数，多列为[]any，元素类型与对应列一致。
- 标准库slices和已有整数解码/无溢出integerOrder足够，无新依赖。内部按同一已验证schema逐成员比较uint64排序值，禁止拼接十进制字符串、相加/哈希或用float64比较；不将不同列类型跨位置互比。低/高边界采用可空元组，nil表示无界，MIN_REC继承边界。
- 叶子物理顺序为主键成员（索引声明序）、事务ID/回滚指针、剩余用户列（原声明序）；主键为非NULL定长整数，非主键NULL/变长数组相对顺序保持。非叶子读取完整主键元组和4字节子页号，不附加事务字段。SDI核对所有显式键、系统列和普通列的位置/长度/hidden属性。
- 验收：新独立库真实单叶子/空表/多层树、非连续列位置、全部整数宽度/有无符号及64位端点、共享前缀、第二/第三列决定顺序、混合NULL/变长/LOB；SQL ORDER BY全主键独立对照，自动/手工schema一致；重复元组、错误子范围/截断/元数据/DESC拒绝且无部分结果；官方CRC/SDI/CLI，race/vet/fuzz，手册51–52。兼容201个既有资产。

- 实测根MIN_REC存储(0,0,2)，其叶子从(0,0,0)开始，因此复合键仍保持哨兵继承边界语义；真实16成员三层树通过完整元组导航。阶段24验收完成，旧单列资产回归通过，未启动25。

## 2026-09-17：第二十五阶段多类型聚簇键

- 问题：正确读取变长/二进制/字符及其他选定类型主键，并按实际排序语义校验树。用户确认本阶段字符串键只支持utf8mb4_bin/utf8mb3_bin/ascii_bin/latin1_bin；其他collation包括默认255作为主键时明确拒绝，不改变其普通列读取。
- 支持矩阵：已有整数，BINARY/VARBINARY，CHAR/VARCHAR（上述四规则），DECIMAL、BIT、DATE/YEAR、DATETIME/TIME/TIMESTAMP（已有精度）。每个键成员支持ASC/DESC；FLOAT/DOUBLE、ENUM/SET、TEXT/BLOB前缀、JSON/空间键仍拒绝。非NULL完整键1..16成员，声明最大键字节总数<=3072，不支持零声明宽度键，允许变长键的实际空值。
- 新Column.Collation string仅字符键填写明确规则，Column.Descending bool仅键使用；原PrimaryKey/PrimaryKeys保持。自动schema仅为字符键填写Collation并从DD order=2/3生成方向，普通字符列schema兼容。NodePointer.Key沿用成员的公开返回类型；二进制为独立[]byte，文本为字符串、CHAR去尾空格。
- 标准库bytes、strings和已有值解码足够，无新依赖。用户选择不移植UCA；拒绝用Go字符串比较冒充collation。比较保存原始主键成员字节：已验证定长类型按编码序，binary按字节序，四个_bin按原编码字节+虚拟0x20补齐比较（包括控制字符小于空格）；每成员DESC只反转比较符号，不反转载荷字节。源码rem0cmp.cc:319–385及ctype-bin.cc:175–209为依据。
- 叶子长度数组按物理序读取：全部键，再非键列；NULL位仍仅对应非键可空列。非叶子读取键的变长长度及保留NULL位图，接完整键后读取子页号；主键页外引用和前缀布局拒绝。所有值先按现有解码器验证，比较不能绕过非法编码检查。
- 验收：各类型/四规则真实单页与树、非连续列序、长短/空二进制、大小写/重音/中文/尾空格/控制字节、混合方向/重复前缀、其他类型端点及精度、普通LOB；SQL完整ORDER BY/HEX、schema和官方CRC/SDI/CLI对照，损坏/无部分返回、race/vet/fuzz，手册53–54；保持207旧资产回归。

- 第二十五阶段实测完成：四规则PAD SPACE和latin1转码前排序、DESC正常载荷、非叶子变长键均与SQL一致；TIME ±838:59:59不得带非零小数，生成器改用合法带小数值。验收结果见[阶段 25 历史记录](../HISTORY.md#stage-25)。

## 2026-09-17：第二十六阶段范围核对（待确认，未作新实现决策）

- 问题：正确识别无显式主键表的实际聚簇索引，读取唯一非空键或隐藏六字节DB_ROW_ID，并保持用户Values不混入系统列。
- 范围冲突：阶段26计划要求多个唯一索引和可空唯一索引验收；阶段20及现行范围规定有二级索引则拒绝读行。已向用户询问是否改为允许只扫描聚簇索引、二级索引仅报告入口。回复前不放宽拒绝契约，不启动依赖此决策的实现。
- 源码核查：ha_innodb.cc:14820–14860选择显式主键或is_candidate_key的唯一键；无候选时14937附近增加隐藏DB_ROW_ID和DD隐藏PRIMARY；dict0dd.cc:3187–3207创建运行时GEN_CLUST_INDEX。DD隐藏PRIMARY与运行时名称不同，不能仅按名称判断。
- sql/dd/impl/types/index_impl.cc:351–389排除可空/虚拟/几何/不完整前缀候选；不能跳过不支持的真实聚簇键而擅自选择另一个支持的唯一键。dict0boot.ic:55–69以mach_read/write_from/to_6读取/写入六字节ROW_ID。
- 已搜索现有key.go/record.go/tree.go/metadata.go、go.mod及标准库能力：现有大端字段、变长键与完整树范围代码可复用；encoding/binary及手动六字节累积足够，无需新依赖。不另建树引擎，不把ROW_ID伪装成用户列，不用SELECT自然返回顺序作为SQL对照。

## 2026-09-17：第二十六阶段实施决策（用户已允许）

- 用户明确允许含二级索引表读取聚簇数据，覆盖旧拒绝规则；仅支持普通BTREE二级索引的入口报告/根校验，不解析二级记录，不支持FULLTEXT/SPATIAL/生成列等其他布局。
- 问题：识别实际聚簇身份并完整恢复无显式主键表的行。标准库及现有记录/树代码足够，无新依赖；拒绝另建树引擎、伪造用户ROW_ID列、按不支持类型过滤候选后选择另一索引。
- Schema新增ClusteredKey *ClusteredKey（JSON clustered_key），与PrimaryKey/PrimaryKeys互斥。ClusteredKey含Name（DD索引名）、Columns（唯一非空索引的有序成员）及HiddenRowID（隐藏键时true且Columns为空）。旧显式主键schema保持不变。隐藏键不能通过省略所有键声明来默默启用。
- Record.RowID *uint64只对隐藏键有值，精确保留六字节无符号数；nil表示该布局不存在ROW_ID，Values不增加列。原始六字节可由PageNumber/Offset定位，NodePointer.Key为uint64。叶子ROW_ID位于origin，后接TRX_ID/ROLL_PTR及全部用户列；非叶子为6字节键+4字节子页号，按原始大端字节比较，沿用MIN_REC和范围校验。
- InspectTable核对SDI首个聚簇索引及完整物理字段列表，分辨显式PRIMARY、首个实际唯一非空聚簇索引、隐藏PRIMARY/DB_ROW_ID（运行时名GEN_CLUST_INDEX）。报告IndexMetadata.Clustered/Hidden；不能因另一个索引更易解析而替换真实聚簇入口。隐藏系统列类型/长度/属性及元素顺序必须严格匹配。
- 验收：真实无索引/空表/重复用户行/NULL/LOB/多层ROW_ID树；单一与多个唯一索引、可空索引先声明、前缀唯一索引、字符混合方向及显式主键+二级索引；SQL无顺序结果用多重集合，唯一聚簇键按ORDER BY比较；官方CRC/SDI、自动/手工schema、旧351资产（2个旧二级拒绝样本转成功但原资产不改）、损坏/无部分结果、race/vet/fuzz、手册55–56。

- 实例启用了sql_require_primary_key；仅夹具生成会话关闭该变量及sql_generate_invisible_primary_key，确保真实测试隐藏六字节ROW_ID而不是自动生成的用户不可见BIGINT主键；不改全局变量。

- 实测SDI隐藏索引为name=PRIMARY/type=2/hidden=true，首元素DB_ROW_ID也hidden=true且length=UINT32_MAX；运行时SQL字典名GEN_CLUST_INDEX、TYPE含clustered位。多个唯一索引的DD顺序与SQL声明顺序不同，实际聚簇索引排首位。以完整字段列表和SQL字典对照验证身份，不以最小名称或用户列名猜测。

- 阶段26按用户确认范围完成。旧两份二级索引拒绝快照转为聚簇读取成功，历史manifest和文件不改；具体矩阵和验证见手册56。未启动阶段27。

## 2026-09-17：第二十七阶段受控更新/删除快照

- 问题：从已结束写事务的稳定聚簇快照读取非delete-mark用户行，并独立保留未purge删除记录的物理证据。用户授权按已列阶段27实施，覆盖此前只插入限制；不以单个ibd推断提交/MVCC可见性，不扩展更新后的复杂LOB或INSTANT布局。
- 已检索现有record.go/tree.go/page.go及Go标准库：现有记录链/空闲链、变长字段、CRC、键比较与bytes切片复制足够，无新依赖。拒绝为更新重写树引擎、把delete-mark直接当可恢复SQL历史行、从free残留猜测完整字段。
- 普通叶子允许0x20 delete-mark，其余instant/version/min位仍拒绝；非叶子规则不变。链/目录/堆编号/garbage/范围仍对所有在链记录验证，不先过滤删除记录。Result.Records仅返回未标记行；Result.DeletedRecords新增独立DeletedRecord摘要，含页/偏移/头、完整键、ROW_ID、事务原字段及独立Raw本地记录字节，不含Values，不跟随删除记录的外部引用。
- 删除记录仍按schema计算本地布局并验证完整键，但普通非键载荷只保留原始字节，不把未知历史LOB/JSON内容伪装成已还原的值。free链仅按既有结构校验，不作为历史行输出。Page.Records是物理在链记录数，可能大于该页返回用户行数；全叶子跨页顺序也覆盖删除记录。
- 真实验收使用新独立库，两会话：只读一致性视图先建立且不访问目标表，用于阻止旧版本purge；写会话完成并提交更新/删除后FOR EXPORT快照，独立当前SQL预期。随后释放读视图，在有限等待内采集purge后的同表快照。只读视图是保留旧版本的测试手段，不改变解析器的事务语义。
- 验收矩阵：定长/变长增长缩短、NULL切换、删除尚未purge/已purge、删除全部、主键更新/删除后重插、键方向和隐藏ROW_ID；真实多页树删除引起的页数减少/合并与根变化对照。SQL当前行、有序/多重集合、官方CRC/SDI/CLI、自动/手工、原361资产、损坏/无部分返回/race/vet/fuzz、手册57–58同步交付。复杂更新LOB仍留阶段28。

- 阶段27实测完成：主键更新留下旧键delete-mark，删除重插同键可复用而不留下独立摘要；增长/NULL切换使origin变化，purge使链记录减少和garbage增加，随后复用保留碎片。三层树根页号4保持但level2降到0。详细原始字节与验收见手册57–58；复杂更新LOB仍留阶段28。

## 2026-09-17：第二十八阶段更新后的 LOB 当前值

- 问题：恢复受控稳定快照中更新后的 TEXT/BLOB/JSON 完整当前值，取消初始未修改 LOB 限制。已搜索 Go 标准库及现有 lob.go/json.go：encoding/binary、bytes、sort 和现有页读取足够，无新依赖。拒绝重写树读取器、按物理页号拼块、把修改事务强制等同创建事务，或无 undo 承诺历史 MVCC。
- 当前行引用版本必须等于 FIRST 当前版本且非零；比 FIRST 新为损坏，比 FIRST 旧为未支持历史读取。活动块版本可小于等于当前版本，历史链只验证结构/版本、不输出历史值；小修改可能不增版本且旧字节只在 undo 中，因此不能仅凭版本号恢复历史行。引用 non-owner/inherited 标志为生命周期信息，允许读取；being-modified 仍拒绝。FIRST 已知 flags bit0 表示禁用后续部分更新，允许；未知位拒绝。
- 按活动 FLST 顺序读取当前块，第一块允许迁移到 DATA 页；索引分配链可含历史及空闲槽位，不再按活动块数推算精确页数。校验地址/双向链/端点/全局槽位唯一性、版本非递增、长度、空间和 CRC；不读取历史块载荷。保留现有 Chunks 来源 API。所有遍历以文件页数及已分配槽数限定。
- JSON 容器允许删除/替换遗留的空洞和载荷重排，以排序后的占用区间验证头/键/值互不重叠，继续验证键顺序、UTF-8、容器长度、节点及深度；空洞不当作节点解释。类型树 API 不变。
- 验收：原377资产；新增独立库 TEXT/BLOB 全量替换、增长/缩短/NULL/空值、继承与删除，JSON 小范围原位修改/大范围块替换/多次更新/删除与空洞重用、跨块字符及多索引页；保留旧读视图与释放后快照，SQL 字节或精确语义、自动/手工 schema、官方 CRC/SDI/CLI、损坏无部分返回、race/vet/fuzz及手册59–60。

- 真实大块更新核查：lob0update.cc:409–415 的整块替换调用 alloc/write，不写 DATA trx 字段；lob0pages.cc:145–185 证实该路径不能要求页事务等于索引创建事务。只对版本1块保留创建事务相等检查，更新版本依赖索引身份/CRC/长度。第一次采集仅作诊断，未计入交付资产。

## 2026-09-18：第二十八阶段验收完成

- 真实小修改保持版本1；大修改第一块由FIRST迁到DATA。活动块版本可以混合2/4。多轮JSON更新中CHAR返回binary opaque导致首次整值替换，其后13次部分更新累积286历史项，不能把14条SQL等同同一LOB递增14次。SQL预期保留原始JSON数字文本，离线验证使用精确数值。
- purge后历史项清空但额外INDEX分配页保持，已以真实快照断言；不按当前块数限制分配页数。历史载荷不读取，仅校验索引项的页号/长度范围和链结构。
- 验收结果集中见[阶段 28 历史记录](../HISTORY.md#stage-28)。

## 2026-09-18：第二十九阶段准备

- 问题：根据行版本和列物理位置读取当前用户列，而不是按当前DDL顺序套用旧记录。已检索现有schema/record/metadata/tree解码器、encoding/hex和sort：现有标量解码与标准库足够，无新依赖。拒绝另建一套类型解码器、用当前SQL默认值替代ADD时默认值、跳过已删除列的物理长度。
- 先以新独立库采集原始SDI/DDL样本，确认物理位置、增加/删除版本及默认字节的实际编码，再固定API细节。支持16KiB DYNAMIC 8.0.45原生行版本；旧式0x80列数格式及升级混合元数据明确诊断为未支持，另需兼容版本实测，不把合成样本当旧版本真实验收。
- 验收：多轮ADD/DROP及交错写入、非末尾/同名重增、NULL/非NULL/空值和类型默认、默认变更不追溯、多页树与隐藏键、丢弃列长度/LOB、非重建及重建清零；保持394旧资产、SQL精确结果、官方工具、损坏/race/vet/fuzz和手册61–62。

### 第二十九阶段实现约定

- 新增可选Schema.Instant（InstantLayout），含当前Version和所有用户物理字段Fields；每项用Position表示完整聚簇字段位置，Column指向当前逻辑列；已删除列用Column=-1与DroppedColumn保存类型，Added/Dropped表示生存版本区间。Default保留ADD时的InnoDB原字节或NULL，不使用当前DDL默认值。原非INSTANT schema保持不变。
- Record.RowVersion指针仅表示显式0x40版本字节，nil表示无该字节（在INSTANT表中按版本0解释）；DefaultColumns记录由历史默认补全的逻辑列号，TextBytes/CharStorage/ENUM/SET仍保存对应值证据，来源可由DefaultColumns区分。已删除记录仅保留原摘要，不补用户值；已DROP字段只计算物理宽度，不解码或跟随其LOB。
- 每行先选取该版本存在的字段，按physical_pos排序，用既有标量/LOB解码器读取后映射到当前列序。NULL位图仅计算该行存在的可空字段；非叶子NULL位图按版本0的可空字段数，含后来删除的初始列，不能用当前列数。保留既有键和范围校验。
- SDI删除列可缺table_id（真实样本证实），其Hidden=2且有version_dropped；不能把任意hidden列当DROP。系统字段physical_pos必须匹配固定键/事务布局；位置重复/缺失、生命周期或默认不完整时明确报错。版本最大64，旧式instant_col/0x80与升级混合格式保持拒绝。

### 第二十九阶段实测与收尾

- ADD时default字节与当前DDL默认独立：改77为99后旧行仍补77。DROP后的原生隐藏列无table_id，新增后再DROP的列可不保留default；只需其生命周期和物理宽度，当前值不恢复DROP列。
- 非叶子按版本0可空字段预留NULL位图，真实8→17→9可空列变化中非叶子始终1字节，重建后变2字节。版本64真实记录的0x40版本值与0x40头标志相邻，分别核对，拒绝65及本阶段未支持的升级版本0。
- 显式夹具schema的类型来自生成器声明，生命周期/默认原字节来自官方SDI的独立Python序列化；不声称两份schema是独立元数据来源，独立正确性依据为SQL值与实际字节/拓扑/损坏验证。
- 验收结果集中见[阶段 29 历史记录](../HISTORY.md#stage-29)。

## 2026-09-18：第三十阶段准备（返回契约待确认）

- 问题：在普通、STORED、VIRTUAL和不可见列混合的表中正确映射物理字段，恢复实际存储值并明确区分未物化与SQL NULL。
- 已检索go.mod、metadata.go、schema.go、record.go、instant.go与现有标准库解码路径：无第三方依赖，已有DD生成表达式/is_virtual解析与标量/LOB解码可复用；无需SQL表达式引擎。本阶段不实现表达式求值。
- 官方8.0.45源码sql/dd/types/column.h:95–106区分普通、引擎隐藏、SQL隐藏和用户INVISIBLE；storage/innobase/dict/dict0dd.cc:3385–3399在物理列遍历中跳过VIRTUAL和系统列。不能把用户不可见列当DROP列，也不能把VIRTUAL计入聚簇载荷。
- 待确认：含VIRTUAL表的读取接口。推荐保留Read/ReadAuto完整行严格契约，另提供显式仅物化列入口；备选为Values保留VIRTUAL位置并返回专用未物化类型，或只报告并拒绝含VIRTUAL表读取。不能静默省略或填nil；nil已有SQL NULL语义。用户答复前不实施依赖此选择的代码。
- 验收：普通/STORED/VIRTUAL/INVISIBLE混合、NULL/变长/LOB、多页树、隐藏或不可见键、INSTANT组合及拒绝边界；真实指定列SQL值、官方CRC/SDI、自动/显式入口、损坏无部分结果、原411资产回归/race/vet/fuzz与手册63–64。具体API及支持矩阵在确认后追加。

## 2026-09-18：第三十阶段实施（用户已确认）

- 用户选择默认完整行严格拒绝VIRTUAL，新增显式仅物化列入口；覆盖准备条目中的待确认状态。
- 采用Schema.Columns只描述当前物化列，新增VirtualColumns描述未物化列的名称、SQL序号和表达式；Column增加Invisible与GenerationExpression。Read发现VirtualColumns即ErrUnsupported，ReadMaterialized显式接收同一schema并返回列定义、虚拟列说明和完整物理读取Result。ReadMaterializedAuto从SDI发现；元数据Schema仍在Issues非空时nil，新增MaterializedSchema仅在所有物理布局受支持时提供。
- 不修改Values的已有值类型或用nil冒充未物化；仅物化结果的Values及所有列号元信息统一以返回Columns为准。用户不可见列仍是物化列，包含在返回中；不计算STORED表达式，仅恢复存储值。VIRTUAL表达式仅报告，不执行。
- 复用记录/树/INSTANT/LOB解码；物理映射先剔除VIRTUAL，再验证完整聚簇字段、系统列和行版本。普通二级索引仍只核对入口，不读取虚拟列索引载荷。函数索引的内部隐藏生成列保留拒绝，不将其当用户VIRTUAL。
- 验收沿用准备条目的真实混合列/NULL/LOB/树/键/INSTANT、SQL/官方工具/全量回归/损坏/race/vet/fuzz及手册63–64；不扩展任意投影或表达式引擎。

## 2026-09-18：第三十阶段真实验收完成

- 实测VIRTUAL没有聚簇element，也没有INSTANT physical_pos；先构造完整物化列映射再复用原解码器。tree的九个VIRTUAL不计NULL位，实际九个物化可空列使用两字节，真实非叶子与叶子均核对。
- 用户INVISIBLE可作为主键，STORED唯一非空键可以成为实际聚簇索引；普通VIRTUAL二级索引仅报告/核对根，不补虚拟值。隐藏ROW_ID重复行保持独立身份。函数索引内部hidden=3明确拒绝。
- 原实例未提供凭证，改用临时目录的同版本实例，仅Unix socket、禁用网络；未改原实例认证或数据。采集完成后已通过mysqladmin关闭临时实例。
- 验收结果集中见[阶段 30 历史记录](../HISTORY.md#stage-30)。

## 2026-09-20：第三十一阶段COMPACT实施契约

- 问题：在8.0.45受控稳定快照中还原COMPACT本地前缀及旧式BLOB链组成的完整当前值。已检索Go标准库及现有schema/page/record/lob/SDI：encoding/binary、bytes、现有CRC和类型解码足够，无新依赖。复用compact记录头和树引擎，不复制整套解析器；拒绝按页号拼接、把旧引用偏移当新LOB版本、或宽松接受未知旧页类型。
- Schema新增RowFormat，省略保持DYNAMIC；显式COMPACT对应SDI row_format=5及非atomic表空间，默认DYNAMIC既有schema不变。ReadSDI允许两种受限表空间读取原始元数据；REDUNDANT仅凭空间标志不能区分，用户读行仍拒绝。COMPACT每个完整键成员最多767字节，总键上限沿用3072。
- COMPACT页外字段本地为768字节前缀+20字节引用；引用第三字段为首个BLOB头偏移38而非版本。ExternalField新增Prefix、Format及HeaderOffset，Offset仍定位20字节引用，Length保持引用内的页外后缀长度，Version仅新LOB有意义。旧链Chunks保存页/载荷来源，IndexPage/IndexOffset为0表示没有新式索引项。
- 旧链只接受8.0.45实际写出的FIL_PAGE_TYPE_BLOB(10)、8字节头（长度/next）、严格CRC/页号/空间/容量/总长度/无循环/精确终点；前缀和后缀合计仍受16MiB上限。允许已知non-owner/inherited生命周期标志，不支持修改中引用；删除摘要不追LOB。字符解码在完整拼接后执行，允许前缀截断UTF-8字符。
- 验收：同逻辑数据的COMPACT/DYNAMIC配对，包括页内/页外边界、768前缀、跨页/UTF-8切分、长VARCHAR/VARBINARY/TEXT/BLOB/JSON、更新增长缩短/NULL/空值/删除、树/隐藏键和INSTANT/生成列组合；SQL完整值、官方CRC/SDI/CLI、原425资产、损坏无部分返回、race/vet/fuzz及手册65–66。使用独立临时同版本实例采集，不改原实例；结束关闭。仅覆盖既定8.0.45，不声称兼容任意历史MySQL或COMPACT压缩。

### 第31阶段实测修正：行格式不决定LOB页格式

- 普通8.0.45 COMPACT插入实际得到768字节前缀+新LOB_FIRST引用（第三字段=版本1）。lob0lob.h:228–240的is_big固定返回true；lob0impl.cc:944–952的小LOB旧链分支在普通构建中不会选中。因此不能将COMPACT直接等同旧链，也不能仅凭第三字段区分格式。
- 修正前述初步分派：按实际首个页类型10/24选择旧BLOB/新LOB；COMPACT决定本地前缀，DYNAMIC无前缀，两种格式均可读取受验证旧链或新LOB。新LOB仍按原版本契约，旧BLOB第三字段必须为头偏移38，最终在拼接完整值后验证类型。完整长度上限包含前缀。
- 官方同版本mysqld-debug提供lob_insert_noindex调试点（lob0lob.cc:461），可只在隔离会话强制旧链写入。新增独立临时debug实例/新库采集，并明确标记为官方调试路径真实写出，而非普通8.0.45默认行为；无手工伪造旧链。普通配对资产与调试路径资产分别统计，旧MySQL版本兼容仍不承诺。

## 2026-09-20：第三十一阶段验收完成

验收正文已合并至[历史验收记录](../HISTORY.md#stage-31)。

## 2026-09-20：第32阶段准备与接口选择

- 问题：避免调用方必须把整表行及全部LOB同时保存在内存，同时保留现有完整校验和Read失败时不返回部分结果的契约。
- 已检索go.mod、tree.go、page.go、lob.go、compact.go、materialized.go以及本机Go标准库context.Context/io.ReaderAt：没有第三方依赖；标准库足以提供取消、同步回调和有界缓存。已有Read在树遍历过程中累积Pages/Nodes/Records/DeletedRecords；LOB路径还保留完整载荷和全部分配索引页。仅在Read外包装回调不会降低峰值内存。
- 图谱查询iterator/cache/reader/blob只覆盖Java参考实现；当前Go实现的修改依据仍为直接读取源码，不把Java接口或图谱边当Go调用事实。
- 推荐同步逐行回调，复用现有Record完整值模型和16MiB单值限制；另提供独立原始LOB分块入口，支持新LOB/旧BLOB/COMPACT前缀，分块可能切断字符或JSON结构。单独块流不冒充已解码完整列。另一方案为流式行中的LOB改为延迟读取句柄；它适合直接处理超大字段，但增加值模型、源文件生命周期和完整类型校验时机的复杂度。该公开接口选择已向用户提问，答复前不实现依赖它的代码。
- 不采用先Read后遍历、无界后台goroutine/channel、为流式复制整套记录/树/LOB校验器，也不让现有Read开始返回部分结果。首轮采用同步、无后台任务的模型，回调可以自然形成背压。
- 流式错误协议建议：只有遍历结束并完成末尾链校验才算完成；取消、中途I/O/结构错误、预算耗尽或调用方提前停止均不能标记完整。已交付回调的数据不可撤回。区分完成状态与处理计数，包装错误继续支持errors.Is。原Read仍错误返回nil。
- 取消只能在页读取、块遍历、回调等边界合作检查；io.ReaderAt本身没有context参数，不能保证中断任意阻塞的底层ReadAt。调用方继续管理文件和快照生命周期；接口不关闭其ReaderAt。
- 资源控制须明确计量对象：有界页缓存、遍历/读取预算、单行物化字节和遍历状态；不能把缓存字节上限宣称为Go进程RSS硬上限。调用方保留回调值的内存由调用方承担。全局重复页/项检测仍需保留受预算约束的状态，不声称严格O(1)内存。
- 验收：现有成功夹具与Read逐行/物理来源对照，既有拒绝输入保持拒绝；覆盖COMPACT、更新LOB、INSTANT、物化入口、删除摘要和隐藏键。加入首行提前停止、中途I/O失败、取消、预算边界、缓存淘汰、尾部校验失败及保留输出所有权测试；大表基准区分累计分配与峰值存活内存。LOB块拼接与既有完整值一致，含跨UTF-8边界，超16MiB原始流验收不得自动放宽类型物化限制。同步交付手册67–68、示例、race/vet/fuzz及可复跑基准。

## 2026-09-20：第32阶段实施（用户已确认推荐方案）

- 用户确认逐行完整值回调＋独立原始LOB分块接口，覆盖上一条待确认状态。公开入口采用Scan/ScanAuto、ScanMaterialized/ScanMaterializedAuto及StreamLOB；同步回调、无后台goroutine、不关闭调用方文件。
- ScanEvent分别交付Page、Node、Record、DeletedRecord，单次仅一个非nil；当前记录仍为现有完整Record，显式物化入口的列定义单独在扫描报告中返回。报告包含Complete和已交付计数，只有末尾校验全部完成才Complete=true。回调返回ErrStopped可主动结束，返回错误保持errors.Is，失败报告不冒充完整结果；计数表示已调用回调（含返回错误的那次）。
- ScanOptions规定有界LRU缓存页数、页访问次数（命中也计数）、累计遍历项、行数、单行源字节及LOB流字节预算；零使用文档化默认值。预算是资源组成上限，不是RSS硬限制。取消在I/O前后及回调/遍历边界检查，不能打断任意阻塞ReaderAt。
- 抽取一个共用树遍历器；Read通过内部收集器保留原子契约与旧默认限制，不套用新流式默认预算。新旧LOB共用逐块校验核心，物化路径在其外累积完整值；旧16MiB限制保留。新LOB仅保留索引页身份，按需重读并受缓存控制，不长期持有全部索引页载荷。
- StreamLOB接收ExternalField中的原始Reference和Prefix，重新解析引用，不信任派生Chunks/Version等字段；Prefix仅允许0或768字节。调用方负责该原始引用和前缀来自稳定、可信的记录。原始块保留逻辑偏移及物理块来源，前缀独立标记；不承诺文本/JSON类型校验。默认流预算64MiB，可显式提高，始终受32位引用长度和遍历预算约束；类型物化仍最多16MiB。无需先成功物化LOB，调用方也可使用可信原始引用调用。
- 验收沿用准备条目，增加所有交付事件重组与Read完全一致、回调数据所有权、缓存读取计数、超过16MiB现有真实资产的原始块流，以及流式/物化存活内存基准。无新文件格式扩展，无需新增MySQL实例或修改原始夹具。

## 2026-09-20：第32阶段验收完成

验收正文已合并至[历史验收记录](../HISTORY.md#stage-32)。

## 2026-09-20：第33阶段准备（范围顺序待确认）

- 问题：利用聚簇B+树直接定位一个键或连续键范围，避免先扫描整表再过滤，同时明确局部校验和查询完成的含义。
- 已检查go.mod、key.go、tree.go、record.go、scan.go和Go标准库sort.Search：无第三方依赖；已有完整键比较、目录所有权校验、树子范围、页缓存/取消/预算和当前行LOB还原可复用。标准库二分查找足够导航。现有key.go只提供物理键解码/比较，用户查询值还需严格归一化；不能把文本显示值或float64强制转换当作精确键编码。
- graphify用图谱已有词汇range/query/key/index查询，只得到Java参考代码；当前Go实现事实以直接源码为准。不会因Java已有查询API而直接移植其校验或字符排序行为。
- 唯一待用户确认的语义：推荐范围上下界按实际索引声明顺序比较，保留每成员ASC/DESC；Reverse只反转输出方向，不交换或重新解释上下界。复合前缀表示完整前导成员相等，不是字符串LIKE前缀。备选为统一自然升序边界，但混合ASC/DESC可能不对应单段物理区间，需额外分解规则和更多I/O。已向用户询问，答复前不实施依赖此选择的查询代码。
- 建议保留第32阶段严格完整行/显式物化视图及同步回调模式，查询报告的Complete仅表示请求范围或limit已完成，不认证整表；记录访问页/请求次数，并区分达到limit与自然耗尽。limit成功终止查询，与ErrStopped主动中断、ErrLimit资源耗尽分开。点查缺失为正常未命中，删除摘要不作为命中用户行。
- 支持矩阵沿用既有聚簇键定义：先实现并验证整数和复合整数，再接入已验证的二进制/字符/精确数值/日期时间比较；不扩展collation、二级索引或SQL表达式。唯一非空聚簇键与隐藏ROW_ID沿用既有布局；ROW_ID查询是物理身份查询，不声称SQL列。
- 实现方向：按根到叶的范围导航剪枝，页内使用已验证目录/键序缩小候选；访问页继续做完整本地结构校验，范围外LOB不解引用。保留路径层级/父子范围及所访问相邻叶子双向链接检查。未访问页不能声明已验证；不先Read、Scan整树，也不另写一套标量/LOB解码器。逆向按树和叶子实际逆序访问，不先收集全结果再反转。
- 验收：存在/缺失/删除键、整数端点、完整复合键与前导列前缀、开闭/无界/空范围、ASC/DESC混合与Reverse、limit/取消/预算、跨叶/跨父页、COMPACT/LOB/INSTANT/物化列；SQL独立值/顺序对照与现有资产回归。记录点查/短范围相对整树的页访问量，注入访问路径与未访问路径损坏区分校验范围，并验证未命中LOB不读取。同步交付手册69–70、示例、race/vet/fuzz。

## 2026-09-20：第33阶段实施（用户确认索引顺序语义）

- 用户确认上下界按索引声明顺序比较、Reverse仅反转输出、复合前缀匹配完整前导成员，覆盖准备条目的待确认状态。
- Query/QueryAuto及QueryMaterialized/QueryMaterializedAuto沿用同步ScanEvent和ScanOptions；KeyRange含Lower/Upper开闭完整键边界、可选Prefix、Reverse和Limit。Prefix与显式边界互斥；nil边界无界，Limit=0不限制。PointKey(key)构造相同的闭区间，缺失为成功零行。使用单一查询核心，不另增一套点查解码器。
- Key为按聚簇键成员顺序的[]any；整数接受Go整数及精确json.Number，其他类型使用现有Read值模型（DECIMAL/时间为规范字符串、字符为UTF-8字符串、二进制为[]byte）。严格校验宽度、符号、精度、字符集及规范时间格式，不接受float64整数、不模拟SQL隐式转换。隐藏ROW_ID使用单个48位无符号值。
- QueryReport嵌入ScanReport并增加LimitReached。Complete仅指所请求范围或limit完成，不表示整表验证；达到Limit是成功，LimitReached=true且不推断后面一定还有行。ErrStopped/ErrLimit/取消/损坏仍失败且已输出前缀不可撤回。范围内delete-mark只输出摘要，不占用户行limit。Page/Node事件是访问路径证据，不保证节点键本身在查询范围内。
- 复用walkTree并增加内部选择器：目录已做完整所有权/键序验证，在目录分组上二分定位候选，再按子范围剪枝。访问页保持完整本地结构和父范围检查，叶子范围外值不追LOB；未访问子树不做完整性声明。正向/逆向均直接改变访问顺序，不全表扫描或收集后反转。无界Query也遵循查询的局部验证契约，不替代严格Scan。
- API接受前已归一化并独立复制查询键。原Read/Scan的全树顺序、链尾及错误契约保持。资源计数包含访问路径/被解码页项；MaxRows仅计实际输出当前行，Query.Limit是查询结果上限，两者不得混淆。
- 实施与验收沿用准备条目，手册69–70同步；标准库encoding/binary、strconv、time、sort已足够，无新依赖。候选解析器/SQL表达式执行、自然升序混合方向区间拆分均不纳入。

## 2026-09-20：第33阶段 SQL 基准的字符集修正

mixed_rows 的第一轮独立 SQL 对照发现 UTF-8 字面量表达式触发隐式字符集/排序规则转换，不能代表 latin1_bin 键的 PAD SPACE 比较。生成脚本改为 CONVERT(CONVERT(X'UTF8HEX' USING utf8mb4) USING latin1) COLLATE latin1_bin，再只读重取40条查询结果；未修改表数据或任何 ibd 快照。原结果保留在 testdata/query/coercion-probe.json.gz 作为诊断证据，不作为正确性基准。查询实现沿用已验证的原编码键比较，不按错误 SQL 预期修改。生成脚本已包含修正，可独立复跑；最终计数见[阶段 33 历史验收](../HISTORY.md#stage-33)。

## 2026-09-20：第33阶段验收完成

验收正文已合并至[历史验收记录](../HISTORY.md#stage-33)。

## 2026-09-21：第34阶段准备（二级记录返回模型待确认）

- 问题：解释普通BTREE二级索引实际存储的字段和聚簇定位信息，保持NULL、前缀截断、重复二级值与delete-mark的区别，不把二级项伪装成完整表行。
- 已检索go.mod、metadata.go的IndexMetadata/IndexElement、record.go的decodePageEntries、page.go、key.go和scan.go，以及Go标准库encoding/binary、bytes、context、io。无需新增依赖；页CRC/目录所有权/堆与活动链、标量解码和扫描预算可复用。现有聚簇记录解码依赖事务字段和完整行，不能仅替换根页号就用于二级页。NULL排序需独立处理，不能将nil与空字节等同。
- graphify对SecondaryIndexLeafPage/SecondaryIndexNonLeafPage/IndexColumn及主键查询只返回Java参考节点；Go修改依据为直接源码核查，不把参考图谱关系当当前实现。
- 本地官方8.0.45源码storage/innobase/dict/dict0dict.cc:3206–3267证实：二级索引先复制用户定义字段，再追加尚未完整包含的聚簇定位字段；只有前缀不算完整包含。同列前缀和完整定位字段可能同时存在，不能按列名简单去重。唯一二级键的SQL唯一成员数与完整物理排序字段数必须区分。
- 待确认的公开模型：推荐独立SecondaryRecord/SecondaryResult和对应扫描事件，按实际字段序返回值，并给出字段来源、声明前缀范围、原始字节、聚簇定位键或ROW_ID、delete-mark及页内来源。nil只表示SQL NULL；未存储列不进入该字段列表。聚簇定位字段可引用同一物理字段，避免误判重叠。前缀标记说明索引只存声明前缀，不据此断言每条原值都确实被截短。
- 备选：继续使用Record.Values并增加二级标志/字段映射。类型数量较少，但其“按完整表列序”的既有契约与二级物理字段序冲突，空缺列容易被误认为NULL，同列前缀与完整定位字段也难以清楚表达，因此不推荐。不以隐式回表补齐Record；回表/投影已安排第35阶段。
- 拟提供完整结果入口和同步扫描入口，按索引名从SDI选择或使用可信显式索引布局；完整入口错误无部分结果，扫描保留第32阶段取消/预算/Complete约定。完整解析选定二级树，不在本阶段加入范围检索、SQL表达式或自动回表。
- 支持范围沿第34阶段计划：先整数唯一/非唯一、NULL、重复值、复合键与聚簇重叠，再验证已有字符/bin排序、DESC、前缀、隐藏ROW_ID及delete-mark；DYNAMIC/COMPACT与物化列布局按现有版本边界核对。非普通BTREE、函数/多值/空间/全文索引不暗中降级。
- 验收：受控新库真实稳定快照，保存原始SHA、DDL、指定索引SQL结果及官方SDI；覆盖空树、多层树、NULL与空值、唯一可空重复、混合方向、字符多字节前缀、重叠字段、隐藏ROW_ID与删除摘要。检查叶子/导航布局、页链/父子范围/完整物理键序；损坏测试只改内存副本。新增独立SQL与官方CRC/SDI/CLI验证，现有资产回归、race/vet/fuzz，并交付手册71–72的实际字节讲解。
- 唯一待用户决定的问题是是否采用独立二级记录模型；答复前不写依赖该选择的实现代码。其余工程细节在以上范围内按实测收敛。

## 2026-09-21：第34阶段实施（用户确认独立二级模型）

用户已确认独立二级记录模型，覆盖上一条待确认状态。公开SecondarySchema描述实际物理Fields、UserFields数量、ClusteredFields定位映射与索引身份；SecondaryField包含原列定义、物化列下标（ROW_ID为-1）、PrefixBytes与RowID标记。SecondaryRecord按Fields顺序给出Values/FieldBytes、独立Raw和页/记录来源、ClusteredKey/RowID及DeleteMarked；非叶子以SecondaryNode单独描述。前缀声明表示字段可能不完整，同列前缀和完整键可以同时出现；不补未存列、不构造事务字段。

提供InspectSecondary（SDI按索引名发现）、ReadSecondary/ReadSecondaryAuto及ScanSecondary/ScanSecondaryAuto。Read错误返回nil；Scan同步交付Page/Node/Record/DeletedRecord并返回SecondaryScanReport，沿用取消、预算、Complete与计数。模型不返回完整表行，因此非索引VIRTUAL不需要求值；索引涉及VIRTUAL或其他未支持字段仍明确拒绝。元数据入口保持既有表布局支持边界，使用MaterializedSchema核对物化来源。

复用页读取、CRC、目录/堆/链校验、标量解码及预算；二级特有NULL位图、无事务字段、完整物理排序/聚簇定位映射使用独立小核心，不用聚簇行解码器猜测偏移。parseIndex的记录数粗边界需允许更短二级叶记录，保留聚簇默认边界。字段NULL与零长字节分别保存，升序NULL最小、DESC反向。完整物理键包括聚簇定位后缀，不用唯一约束成员数截断导航键。原资产只读，新采集使用隔离实例和新库。验收及手册71–72沿用准备条目。

## 2026-09-21：第34阶段真实布局补充

- overlap快照实测：SDI的prefix_idx只有code前缀与id，而物理字段还有完整code。ha_innodb.cc:14949–14978的DD补列逻辑按列身份去重；dict0dict.cc:3223–3249的内部字典按是否完整包含去重。分别重建SDI预期和实际Fields，不能直接将SDI元素数组当全部物理字段。完整副本继承聚簇键方向，已完整存在的字段沿二级声明方向复用。
- wide_prefix与compact的128..200字节前缀证实：长度头使用原列的DATA_BIG_COL容量判定，而不是缩短后的前缀上限。rem/rec.cc:114–143给出同一规则。先按原列格式读长度，再检查实际长度不超过前缀范围；字段解码按前缀定义处理字符数和固定宽度。
- 本阶段前缀限已有键类型CHAR/VARCHAR/BINARY/VARBINARY，TEXT/BLOB索引前缀暂不扩展；其他已有标量键沿用原类型支持。NULL参与物理排序，完整物理键严格递增；唯一索引额外检查未delete-mark且用户成员均非NULL时的用户键唯一性，不把可空唯一约束当物理唯一排序键。

## 2026-09-21：第34阶段验收完成

验收正文已合并至[历史验收记录](../HISTORY.md#stage-34)。

## 2026-09-21：第35阶段准备（前缀索引查询语义待确认）

- 问题：利用二级树定位需要的行，完整值足够时直接投影，否则按聚簇定位键回表，并保证前缀碰撞、NULL和重复二级值不会造成漏行或假命中。
- 已检查go.mod、secondary.go、secondary_schema.go、query.go、query_key.go、tree.go和scan.go，并检索Go标准库sort、bytes、context、io：无第三方依赖，已有二级完整物理比较、目录搜索、聚簇点查、严格键编码及共享缓存/预算可复用。现有walkSecondary只支持整树扫描，Query物化完整聚簇行；不能用扫描后过滤冒充索引查询，也不能让投影无条件读取无关LOB。
- graphify查询SecondaryIndex/query/projection/clustered key仅返回Java参考关系；当前实现依据为直接源码。只读核查本地官方storage/innobase/row/row0sel.cc:116–155、266–284，前缀长度需按字符编码截取，二级值与聚簇值比对必须区分前缀，不将截短值当完整列。
- 待确认的语义推荐：对二级声明列输入完整原列值和开闭边界，按各成员ASC/DESC比较；遇前缀字段先构造保守候选区间，取得完整值后再精确判断。例INDEX(name(3))查询abcdef：abc只用于导航，abcxyz不算命中。不能在截短后的首字段相等时继续用后续成员收紧候选而漏掉实际满足完整元组边界的行。Prefix沿用完整前导列等值含义，不是LIKE。
- 备选：查询参数直接描述物理索引字段，abcdef超过前缀容量时拒绝，abc表示所有存储前缀等于abc的项。实现更短，适合底层物理检索；但调用方必须自行回表过滤，容易把物理前缀匹配误当列值等值，因此不推荐作为本阶段面向行查询的默认语义。第34阶段仍提供独立物理项读取能力。
- 推荐的其余契约：边界仅包含二级用户声明成员，不要求调用方补聚簇后缀；点查返回全部重复二级值。nil键成员显式表示索引NULL，按物理NULL顺序比较，不模拟SQL三值逻辑；nil边界表示无界。输出按二级完整物理键顺序（含定位后缀），Reverse只反转顺序，Limit在完整条件核验和成功交付后计数。前缀索引输出顺序不冒充原列完整值排序。
- 投影返回独立列清单/Values与来源，不复用表示完整行的Record来填缺失列。指定物化列，完整值在索引中才可覆盖；前缀不能覆盖原列，即使某条值看起来很短。有同列完整聚簇定位副本时可复用该完整字段。条件或投影缺值时回表，按需读取列/LOB；不求值VIRTUAL、不返回伪NULL。范围外记录和未投影且不用于过滤的LOB不读取。
- 回表核对当前聚簇行与二级定位/索引字段，非delete-mark二级项缺少对应当前聚簇行或值矛盾时明确错误，不静默跳过。覆盖路径不宣称验证了未访问的聚簇行，不新增MVCC保证。共用同一缓存、取消和累计预算，不为每次回表重置资源计数；分别报告二级访问、候选、回表、覆盖和最终交付数量。完成协议沿用查询局部范围/Limit契约。
- 排除先全表扫描后二次筛选、无条件回表、独立SQL表达式执行器、重新定义自然升序混合索引边界及二级项伪装完整Record。采用同步回调库接口及示例，沿用严格/显式物化视图边界；不启动通用SQL或第36阶段空间分析。
- 验收：独立SQL对照等值/范围/开闭/无界/空范围、重复值、NULL、复合混合方向、Reverse/Limit、前缀碰撞及后续成员交错；覆盖与回表投影值一致且来源明确；隐藏ROW_ID、delete-mark、INSTANT/STORED与物化列、选中/未选LOB。点查/短范围的实际页访问量证明剪枝；缺失/不一致定位、访问和未访问页损坏、取消/预算/I/O/回调所有权、全量资产回归、官方工具、race/vet/fuzz及手册73–74同步完成。
- 唯一待确认问题是完整原列谓词与物理前缀谓词的公开选择；用户答复前不写依赖此选择的实现。准备工作已完成，后续工程细节在确认的语义内推进。

## 2026-09-21：第35阶段实施（用户确认完整原列谓词）

用户已确认按完整原列值解释查询。采用SecondaryQuery{Range KeyRange, Columns []string}：Range只描述二级声明成员，允许显式nil成员表达NULL；Columns为按调用方顺序列出的物化列名，nil表示全部物化列，非nil空数组和重复/不存在/VIRTUAL列拒绝。公开QuerySecondary/QuerySecondaryAuto及QuerySecondaryMaterialized/QuerySecondaryMaterializedAuto沿用同步回调，返回独立ProjectedRow和SecondaryQueryReport。显式入口同时接收可信聚簇Schema和SecondarySchema；校验两者的列来源/索引身份与定位映射，Auto共享同一个scanReader进行发现和查询。

ProjectedRow只含投影Values、二级来源、可选聚簇来源、Covered及完整定位键/ROW_ID，不伪装完整Record。报告Columns/VirtualColumns、候选/过滤/回表/覆盖/交付计数、二级与聚簇访问量、Complete/LimitReached及共享I/O计数。MaxRows计最终交付行；候选受累计MaxEntries约束，MaxRowBytes涵盖每个候选的本地源字节及实际物化的谓词/投影LOB。Limit仅对成功交付计数，不计删除项和前缀碰撞。

导航在首个参与查询的前缀字段截断边界，并将该截断等价类整体纳入候选，后续成员留到完整值复核；无前缀时使用声明字段精确范围，聚簇后缀仅决定同值输出顺序。复用directorySearch及二级完整页解析，反向实际改变树访问顺序，查询边缘不要求整层首尾；原ReadSecondary/ScanSecondary完整校验不变。

回表使用现有聚簇点查的内部延迟LOB模式，保持完整本地页/记录结构校验，只在查询内部持有尚未物化值的Record；不改变公开Read/Query结果契约。先补齐并核验索引字段/谓词，命中后才物化投影LOB，未命中或未选LOB不解引用。回表缺失/重复当前行、二级键不一致报ErrCorrupt。覆盖路径仅认证访问到的二级数据，不宣称读过聚簇行。使用现有标准库，无新依赖；其他边界及验收沿用准备条目。

## 2026-09-21：第35阶段验收补充

- latin1空字符串编码必须是非nil零长字节，不能与二级NULL混淆；修正既有查询键编码初始化，保持原聚簇比较结果。
- 条件完整值已在索引中时先过滤，即使投影需要回表，也先排除不匹配项；overlap样例120候选只回表1次。
- 默认累计MaxEntries不因回表重置；3000行逐次回表可达到默认100万遍历上限，SQL全矩阵显式使用1000万。保留安全预算和可见计数，不静默扩容。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-35)。

全量race/coverage默认10分钟在新增SQL矩阵期间超时；提高测试进程超时至25分钟后重新执行，不修改产品预算或减少验收案例。

只延长测试进程超时，保留原矩阵和产品预算；最终执行结果见[阶段 35 历史验收](../HISTORY.md#stage-35)。

## 2026-09-22：第36阶段准备（分析失败契约待确认）

- 问题：解释稳定独立表空间的页/extent/segment分配及索引、LOB归属，并给出可交叉核对、分母明确的空间统计。
- 已检查标准库encoding/binary、io、context、bytes、现有go.mod、page.go/checksum.go/scan.go及索引/LOB入口；无第三方依赖，复用CRC和已有布局核心，新建独立分配解析与报告，不移植Java类层级。graphify词汇fsp/xdes/inode/extent/bitmap/page/space检索仅提供Java参考位置，不代表当前Go已实现。
- 本地官方8.0.45源码fsp0fsp.h:130–233、268–343明确FSP_SIZE/FREE_LIMIT、空间链、INODE、XDES状态和分配位。FREE_LIMIT可大于小表实际文件页数；>=FREE_LIMIT页按定义尚未初始化分配信息。不能将文件长度、FSP_SIZE、FREE_LIMIT混为一个计数。既有empty快照文件8页、FSP_SIZE8、FREE_LIMIT64、2全零页；deep快照960/960/576、608全零页；huge快照1216/1216/1088、178全零页。这些是源文件直接读字节的准备探针，不是完整分配验证结果。
- 用户已授权使用现有本地实例及所给凭据；只读连接确认8.0.45、16384、file_per_table=1、crc32。新样本使用新独立库，采集时FOR EXPORT，不修改既有用户表或全局配置，不停止用户实例；密码不保存到项目文件。普通sandbox socket访问失败后经授权提权读取成功。
- 范围：既有16KiB、非压缩非加密、DYNAMIC/COMPACT独立表空间；独立AnalyzeSpace类入口和报告，枚举文件页，解析通用FIL、FSP_HDR/XDES/INODE/IBUF_BITMAP，交叉验证extent位图/双向链/段碎片与范围，关联可验证的index与LOB归属。未知页保留类型、来源和原始信息，不猜其内部布局。合法空闲/未初始化页单独分类，残留页头不作为活动归属证据。
- 指标分别提供文件物理页、FSP大小、初始化边界、extent保留/已用/空闲页、segment整区与碎片页，以及INDEX层级/物理记录数/目录/heap/garbage/连续空闲。不能将garbage等同表空间可分配页，也不将物理记录数称为SQL可见行数。有效字节须说明是布局计数还是经记录解析验证，无法证明的关联保留未知，不生成单一“填充率”。
- 推荐严格分析：在已分配结构或使用中页面出现CRC/LSN/页身份/链/位图矛盾时立即错误，不发布成功报告；已证实空闲、未初始化全零页合法，未知页可保留而不承诺内部验证。保留既有Read错误原子性和未访问页边界，不将此分析器作为损坏抢救工具。
- 备选诊断扫描：继续处理其他页面，输出逐页错误及不完整统计；适合排障，但错误传播、关联未知和统计分母需要额外契约，不能把局部统计视为完整空间结果。此选择会改变公开报告及错误协议，需用户明确；问题已提出，答复前不编写依赖该选择的实现。
- 排除只扫页类型计数代替分配验证、将所有全零页判损坏、将空闲残留页认作活动索引、解析用户SQL/恢复MVCC、通用/系统表空间、压缩/加密、多页大小及第39阶段浏览器。更简单的实施采用同步库API与JSON示例，不提前引入可视化服务。
- 验收：已有资产只读对照，新独立库的空表/增长跨extent及XDES覆盖范围/删除purge后空间复用/重建/多个索引和LOB快照；文件长度、SQL可核对的表空间/索引身份和官方CRC/页统计对照，分配链与位图互证；真实字节与手册75–76同步。损坏链、重复所有权、非法位图/页范围、已分配全零页、合法空闲页、未知页、I/O/取消/预算以及原读行回归/race/vet/fuzz必须通过。

## 2026-09-22：第36阶段实施（用户确认严格分析）

用户选择严格分析。新增AnalyzeSpace(ctx, io.ReaderAt, size, SpaceOptions)返回(*SpaceReport,error)，任一错误返回nil报告；SpaceOptions仅包含MaxPages和MaxEntries（默认1000000/10000000），限制文件页数和链/结构遍历，取消与I/O错误保持可识别。不复用有行/LOB物化含义的ScanOptions。

报告返回文件页清单、FSP字段、extent/segment、索引聚合、类型和空间计数；已用页验证CRC/LSN/FIL身份与已支持布局，空闲页记录原头但不据此归属，未初始化页独立分类；未知已用页保留完整原始页且不承诺内部结构验证。INODE列表、三类空间extent链、段extent链、碎片页与位图互证；显式支持8.0.45的XDES_FSEG_FRAG状态5，描述页/位图保留页不计入租用段数据页。INDEX空间指标按头部布局定义，不声称等于SQL可见行字节；段归属提供索引/LOB页关系，不推断LOB属于某个SQL列或事务版本。

新增分配专用快照使用.space.gz，独立manifest及SQL/官方工具预期；不将大规模空间样本自动纳入既有逐行/逐点查询矩阵，旧487份.ibd/.ibd.gz资产仍全部参加原回归，并另做空间解析回归。真实大样本须跨16384页描述覆盖范围，验证第二组XDES/IBUF_BITMAP及租用extent。生成器新建独立库，保留用户实例运行，采集不改旧快照。手册75–76解释所有分母、合法空闲残留与分析边界。

第36阶段布局核对：IBUF_BITMAP的起点是PAGE_DATA=94，每页4bit，其中free class的两位按源码value=bit0*2+bit1组合，不能当连续二位小端数。索引分析额外验证紧凑记录头、活动/空闲链、heap编号、目录owned、同层双向链/唯一首尾及根层；不解码字段长度、键比较和父子导航指针，因此LayoutUsedBytes明确为heap减garbage的头部推导量。已有完整读行/查询入口继续负责键和字段级验证。

官方innochecksum的页类型dump仅列已识别类型，新式LOB22/23/24进入Other汇总且不生成逐页行；验证应对照已识别页的编号/类型/索引指标，另核对Other数与总页数，而非要求每个LOB都有dump行。

第36阶段最终边界补充：AnalyzeSpace显式拒绝系统space ID0（ErrUnsupported），并把INDEX的ID0判为ErrCorrupt，避免仅靠后续页归属矛盾间接拒绝；测试先复现了不明确的错误分类，再补齐入口/页身份检查。主体全量race运行后，对最终空间代码追加全部空间专项race及fuzz/vet，不改变既有读取模块。

## 2026-09-23：第36阶段验收完成

验收正文已合并至[历史验收记录](../HISTORY.md#stage-36)。

## 2026-09-28：第37阶段准备（导出格式待确认）

- 问题：把现有离线读取/查询/空间分析能力组织成稳定命令行，并使导出结果能无损读回、正确区分完整成功和中途失败。
- 已检查go.mod、metadata.go、scan.go、query.go、secondary_query.go、space.go、JSONValue/GeometryValue及examples/read/query/secondaryquery/space。标准库flag、encoding/json、encoding/csv、strconv、encoding/base64、context、os、os/signal可以完成参数、类型编码、取消及文件发布，无需Cobra或新依赖。graphify现有词汇只匹配reader/page，检索给出Java参考节点，不能代替当前Go实现事实。
- 更简单的实现：cmd/innodb-reader作为薄入口，复用现有解析API；导出编码/解码独立成可复用包，命令执行逻辑可注入context和stdout/stderr以便测试，不改现有示例契约，不重新实现页或SQL解析。
- 命令范围：metadata查看SDI发现报告；page查看指定物理页的分配状态/通用信息（基于严格空间分析，明确全文件校验范围）；export流式导出当前物化行；query支持聚簇或命名二级索引的现有范围JSON与投影；check执行明确范围的完整验证；space输出空间统计/报告。保留VIRTUAL严格拒绝与显式materialized选择，保留资源预算、Reverse/Limit和原索引顺序；不接受SQL表达式。
- 输出格式待确认，推荐无损优先：带版本/列定义及明确值类型，SQL NULL与空文本/空字节分离，64位整数及精确小数用文本，浮点保留宽度与负零，二进制base64；JSON列保留现有类型树及opaque载荷，内部整数不经float64，GEOMETRY保留SRID/WKB。JSONL逐行事件和CSV单元格JSON值编码共用一套值协议，提供对应读回接口；物理来源显式开关，不能将未选择列填NULL。
- 示例推荐单元格：{"type":"uint64","value":"18446744073709551615"}、{"type":"string","value":""}、{"type":"null"}；具体封套在用户选择无损格式后固定。CSV由encoding/csv负责引号/逗号/换行层，单元格的JSON负责类型及NULL层。
- 备选普通表格CSV：列值直接写文本，另存列类型/编码/NULL转义元数据；更适合直接查看，但读取必须同时持有元数据，空串/NULL/转义字符串要单独定义，且多文件发布需要额外一致性协议。不能只靠给CSV单元格加引号保存大整数类型。用户选择会改变公开文件协议，已提出唯一待确认问题，答复前不编写依赖格式的实现。
- 推荐其余契约：stdout仅承载请求数据，诊断与最终执行摘要走stderr；可识别参数错误、读取/格式失败、取消和成功退出码。stdout已发出的前缀不可撤回，取消或写失败必须非零退出，不发送成功完成标记。JSONL可带显式完成事件；CSV完整性需结合执行退出状态/摘要，不能追加伪数据行。
- --output文件采用同目录临时文件，完整成功并flush/sync/close后再发布；失败移除本次临时文件，已有目标保持。默认不覆盖已存在目标，显式覆盖选项才允许替换；禁止输出等同输入文件（含可识别硬链接），不因校验失败截断输入或原目标。一次发布一个自包含结果，避免无损推荐方案引入必须同时提交的sidecar。
- 排除仅拼接旧示例参数、用float64中转键/导出数值、把查询局部成功称为全文件认证、吞掉写错误/管道断开、把CSV空单元格统一解释NULL、SQL dump/恢复导入、在线活动ibd采集与第38阶段分区集合。不会为了本阶段重新采集已有类型齐全的真实资产；如发现必要缺口再在已授权实例的新库补样本。
- 验收：JSONL/CSV经公开解码接口逐值/类型往返，覆盖NULL/空串/空字节、整数端点/超过2^53、DECIMAL、浮点负零、字符集/特殊换行引号、JSON类型/opaque、GEOMETRY、INSTANT/STORED及显式物化视图。真实全表/聚簇/二级结果与既有SQL预期或已验证API一致；metadata/page/check/space结果与现有接口对应。端到端子进程验证帮助、未知参数、退出码、stderr/stdout、预算、SIGINT、流式中途失败、输出写失败、原子发布/清理、目标已存在和输入同名保护；原有回归/race/vet/fuzz及手册77–78同步完成。

## 2026-09-28：第37阶段实施（用户确认带类型无损格式）

用户已确认推荐方案，无剩余格式问题。问题、被排除的普通CSV/sidecar方案及验收沿用准备条目。新增rowio包和internal/cli，cmd/innodb-reader只负责信号及退出；保持解析核心不变。

协议版本1：首记录是schema（version、columns、virtual_columns、physical）；JSONL后续row包含逐列带type的值，最后end包含字符串行数，必须验证end、计数及无尾随内容才能认证完成。CSV首行单元格保存schema JSON，后续每列是同一带类型JSON值，开启physical时多一列；没有伪数据尾行，CSV EOF只能证明语法结束，完整性依赖生产命令退出状态或原子发布。每个协议记录占一物理行，字符串换行由JSON转义。读回默认每条记录128MiB上限，可显式配置；不承诺RSS上限。

整数tag保留Go宽度，十进制字符串载荷；float32/64为对应精度的有限数文本，保留负零；DECIMAL/时间/ENUM/SET沿用string并由Column保留SQL类型；binary为base64，nil只表示SQL NULL。JSON列保留Kind/BinaryType/opaque及递归类型树，标量复用类型编码。空间值保存SRID/WKB并用DecodeGeometry重建树。可选physical使用原Record/ProjectedRow去掉Values后的JSON对象，读回以json.RawMessage保存，避免隐式float64转换；字段中原有字节/整数表示沿用库JSON契约。无损指恢复库当前值及选择的来源，不代表SQL恢复导入或原插入文本。

命令metadata/page/space输出现有报告JSON；page必须显式--number，先完整AnalyzeSpace再选页。check --scope rows（默认）用整表Scan，--scope space用AnalyzeSpace，--scope secondary --index名称用选定二级整树Scan；不能将任一scope称为全库/MVCC认证。export/query支持--format jsonl|csv、--physical、--materialized和ScanOptions预算；query --query路径读取单个KeyRange，二级--index使用SecondaryQuery封套，二进制键为单字段base64对象，UseNumber禁止整数经浮点。

为先发列定义，导出会先InspectTable；预检页读取计入MaxPageReads，再将剩余请求预算交给原API，二级查询仍共享其内部全路径累计预算。SDI容量及遍历仍遵循既有独立限制，预检不计作用户行遍历项。所有参数放在唯一输入ibd位置参数之前。文件输入须是稳定、解压后的快照。

退出码0成功、2参数错误、1运行/读写失败、130取消（SIGINT/SIGTERM）。stdout仅数据，stderr最终JSON摘要明确command/scope/complete/error及API报告；stdout错误前缀不能撤回。文件输出同目录临时文件，编码完成、sync/close后发布，默认硬链接无覆盖发布，--overwrite才rename替换；拒绝覆盖输入及查询文件（含硬链接），失败清理临时文件。发布保证单文件原子可见，不承诺断电后目录持久性。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-37)。

## 2026-09-28：第37阶段验收完成

查询文件打开/读取错误归运行失败退出1，格式错误退出2；查询原文和列元数据非法UTF-8拒绝，避免标准库静默替换。文件身份测试包括未压缩资产在内全部先复制到临时目录，防止保护机制回归时伤及原始快照。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-37)。

## 2026-09-29：第38阶段准备（集合输出顺序待确认）

- 问题：将同一逻辑分区表的完整独立表空间快照集合可靠地还原为带分区来源的行流，并识别缺文件、错误身份及不兼容元数据。
- 已检查标准库io/context/encoding/json/os/path/filepath以及现有SDI、InspectTable、Scan、查询、rowio和CLI原子输出。集合编排可复用现有解析/编码，不增加第三方依赖；若选择全局排序，标准库container/heap可管理归并，但键比较必须复用既有索引语义，不能使用Go字符串或显示值直接排序。graphify已有图谱仅覆盖Java参考，词汇table/reader给出参考位置，不代表Go分区支持。
- 当前metadata.go明确拒绝partition_type/subpartition_type/partitions，且逻辑索引private为空时仅报告入口定义，不能直接读分区用户行。本地MySQL8.0.45的sql/dd/impl/types/partition_impl.cc:260–276明确每个分区序列化number、se_private_id、private、values、indexes、subpartitions及tablespace_ref；partition_index_impl.cc:151–160用index_opx关联逻辑索引，同时保存该分区物理private和表空间引用。必须验证映射，不能靠文件名或把逻辑索引当成所有分区共用根页。字段组合仍需真实SDI快照验证，不以源码片段代替实测。
- 推荐第一版严格完整集合、逐分区扫描：输入显式文件清单，全部预检通过后按分区定义序输出，每个分区内沿现有聚簇键顺序；每条行/事件保留分区身份，物化列顺序不插入伪SQL列。不承诺跨分区全局有序，隐藏ROW_ID也只作分区内物理定位。集合总计页请求/遍历/交付预算，不得切换文件后重置；页缓存必须隔离文件身份。
- 文件清单提供明确路径与分区对应，完整性须由SDI的逻辑表/分区定义交叉核对，不能把用户恰好提供的子集视为完整表。拒绝缺失、重复、额外、错误逻辑表/物理身份、列或索引定义冲突；允许各分区不同space/index/root等物理位置，分别验证自己的布局。单文件入口不得把单个分区误报为完整逻辑表。重建/交换前后错误混配需有拒绝案例。稳定同一采集窗口由调用方保证，元数据一致不证明事务快照一致。
- 保留strict与显式materialized选择、同步回调和Complete契约；发现集合问题在首行前失败，扫描中后续损坏/取消仍可有已交付前缀，最终不得完整成功。CLI复用带类型JSONL/CSV和原子发布，输入集合中每个文件及清单都受输出身份保护。分区来源与普通物理记录来源需清晰区分，具体公开封套在顺序选择后固定。
- 备选本阶段同时支持全局聚簇键归并：需要每分区迭代状态、正确的ASC/DESC/字符及复合键比较、相同键确定顺序、全局Limit/反向与多文件缓存/预算协议，不能通过全量装载再排序绕开流式目标；增加实现和验证范围。该行为是阶段计划中“逐分区或按键合并”的公开语义选择，已向用户提出唯一待确认问题，答复前不写依赖该选择的产品实现。
- 排除仅按#p#文件名拼接、静默缺分区、将多分区拼接称为全局有序、改变旧单文件接口含义。第一版定位普通独立分区文件；不扩展通用/系统表空间、在线采集、SQL分区表达式求值/路由裁剪、事务一致性证明或抢救式部分集合。子分区不自动纳入首版支持承诺，先验证并明确拒绝未支持布局。
- 验收：在已授权MySQL8.0.45实例的新独立库采集RANGE/LIST/HASH/KEY代表样本及空分区、多页/LOB/普通二级索引、重建/EXCHANGE前后快照；保存逐分区与全表SQL预期，官方CRC与ibd2sdi逐对象核对。新增分区资产独立保存，避免被旧单表*.ibd*全量矩阵误当普通完整表。预检覆盖缺失/重复/额外/身份与schema冲突、错误旧新混配；流式覆盖来源、预算累计、取消、所有权、后段损坏、CLI完整标记和原子文件。原有487份行资产及空间/CLI回归保持，race/vet/fuzz与手册79–80同步。无用户旧表/全局设置修改，不停止用户实例，不将凭据保存到仓库。

## 2026-09-29：第38阶段实施（用户确认逐分区扫描）

用户确认推荐方案，无待确认产品问题。采用完整集合预检、分区定义序及分区内聚簇键序，暂不实现全局归并或集合范围查询。问题、被排除方案及验收沿用准备条目。

新增PartitionInput（显式Name、ReaderAt、Size），InspectPartitions以及ScanPartitions/ScanPartitionsMaterialized。集合元数据包含逻辑表身份、共同列定义及逐分区物理身份/索引入口；Inspect错误返回nil。流式先预检所有输入，首次回调交付集合元数据，之后PartitionEvent保留PartitionSource及原ScanEvent；首次元数据回调前不交付用户行。最终报告为累计读取/遍历/页/行计数和分区完成数，错误Complete=false，不撤回旧事件。输入Reader由调用方持有关闭，稳定采集窗口仍由调用方保证。

复用一个scanReader累计预算，切换文件时清空LRU并替换ReaderAt，禁止相同页号跨文件命中。预检计入页请求和已有SDI遍历预算，文件数最多1024，集合累计SDI解压JSON最多64MiB，单对象保持16MiB；防止大量分区复制逻辑定义导致无界保留。各分区必须独立核对space/table/index/root，逻辑定义及分区目录须一致；不通过移除异常元数据来兼容不支持特性。SDI分布与最小支持字段在真实快照探针后细化。

CLI新增--manifest用于metadata/export/check（仅rows范围），与原单ibd位置参数互斥；清单为version=1、files数组，每项包含partition和path，相对路径以清单目录为基准。旧单文件入口契约保持。导出复用rowio版本1：集合导出始终带physical对象，其中partition记录来源；--physical额外包含record来源，SQL列不改变。header的physical=true，预检后再写schema；无需新CSV侧文件或修改原值协议。原子输出保护清单及其全部输入文件。

使用已授权本地实例的新唯一数据库采集，密码只在进程环境中传递，不落盘。分区快照后缀.partition.gz及独立清单/SQL/官方预期，不改变旧单表夹具glob。手册79–80覆盖逻辑定义与分区物理索引映射、来源/顺序/完整性及真实字节和失败案例。

第38阶段真实布局补充：首批RANGE/LIST/HASH/KEY及生命周期快照中，只有number=0分区带Table SDI，其余仅Tablespace SDI；逻辑table.se_private_id为无效ID哨兵18446744073709551615，各分区才有实际InnoDB table ID。集合必须恰好找到一个合法首分区Table来源，并按其完整目录匹配所有文件。对每个已核实的分区，用逻辑列/索引定义与该分区index_opx对应的物理private、tablespace_ref和table ID构造内部普通表视图，再复用既有严格InspectTable映射；不是修改原始SDI，也不放宽旧单文件入口。支持已实测partition_type=1/3/7/8（HASH/KEY/RANGE/LIST），子分区明确拒绝，不执行分区表达式。

旧新快照混配仅在SDI/物理身份矛盾时可检测；未改动物理身份的跨时间DML快照不可由元数据识别，不能声称证明同一时点。额外采集非首分区重建，使旧非首分区文件与新目录矛盾有独立真实测试。采集连接因实例要求主键，显式SET SESSION sql_require_primary_key=OFF以生成隐藏聚簇键样本；未修改GLOBAL。

第38阶段列身份实测：共同columns.se_private_data.table_id始终指向首分区，而非每个分区自己的ID（重建/交换后随首分区更新）。内部元数据适配因此区分共享列所属ID与当前分区物理ID：前者严格核对首分区，后者严格核对索引private/根页；旧单表入口仍使用自己的ID核对全部字段，不放宽。预算准确边界：预检页请求和集合目录/index_opx遍历计入累计ScanOptions；SDI树仍沿用其独立容量和记录上限。

第38阶段独立索引证据补充：12次采集的INNODB_INDEXES关联结果均为空，不能声称已与SQL物理索引ID对照。官方storage/innobase/handler/i_s.cc:5946–5962扫描mysql.indexes，dict/dict0dd.cc:5964–5973在se_private_data为空时跳过；分区物理信息保存在partition_indexes。保留原始SQL证据不补造，将逐分区index_opx/ID/root/space对照改为官方ibd2sdi；SQL仍核对PARTITIONS、INNODB_TABLES和全部行值。

## 2026-09-29：第38阶段验收记录

验收正文已合并至[历史验收记录](../HISTORY.md#stage-38)。

失败/预算补充的验收正文见[阶段 38 历史记录](../HISTORY.md#stage-38)。

新独立数据库innodb_reader_partition_9e39b58bcfab使用用户授权测试实例；仅调整生成会话主键要求，未改全局/原有用户表，采集完成后未停止实例。本阶段无剩余待办，第39阶段未启动。

## 2026-09-29：第39阶段准备（报告深度待确认）

- 问题：将已验证的页分配、索引结构和物理来源转为可离线浏览、能定位真实字节与手册的学习报告。
- 用户授权开始第39阶段；STAGE_PLANS明确先离线HTML，再决定是否需要Web应用。本阶段以自包含HTML/CSS/JavaScript为交付方向，不引入HTTP服务或外部CDN。
- 已检查go.mod（仅标准库）及html/template、encoding/json、io、embed等已有标准能力，复核AnalyzeSpace、InspectTable、Scan/ScanSecondary、Record/NodePointer/ExternalField与CLI原子输出。无需引入图表框架或新物理解析器。graphify现有图仅覆盖Java参考，page/index/tree检索可用于参考导航，不是Go实现证据。
- AnalyzeSpace提供分配、索引层级/根、LSN与局部布局指标；space_index.go明确不解码键、字段长度或父子指针。必须通过已有行/索引解析接口获得真实树边、记录和LOB路径，不能把同层Previous/Next边画成父子边，也不能从页号邻近关系猜树结构。
- 推荐分层报告：默认空间概览，显式选择索引/页后加入B+树、记录偏移、LOB路径与原始字节钻取。空间解析严格失败；显式请求的详细层级也必须成功，不能静默降级为完整成功。空间报告成功并不认证未请求的行或LOB。具体CLI/API字段、预算和包含页规则在报告深度确认后固定。
- 备选每次完整全表详细报告：解析全部受支持索引/记录/LOB并嵌入原始数据，优点是一次获得全面钻取；代价是更高I/O、内存和HTML大小，行解码不支持会阻断原本可用的空间学习视图。排除自动吞掉详细解析错误、全量物化后无界嵌入、外部网络加载和为绘图另写物理解析器。
- 唯一待确认问题：采用分层报告还是每次全表详细报告。已向用户提问；答复前只推进调查和文档，不写依赖该选择的实现。
- 验收：图中页/索引身份、真实父子边与LOB路径逐项对照现有API；页内/绝对/记录相对偏移明确且与固定夹具原字节一致。LSN分布明确不是实时热点，填充率展示公式与分母；未知使用中页和空闲残留不虚构已验证字段。检查HTML转义、大整数精度、离线打开、导航/搜索、预算/取消/损坏/原子发布，使用现有真实夹具保持旧基线，并交付手册81–82。新MySQL采集仅在已有夹具无法覆盖时考虑。

## 2026-09-29：第39阶段实施（用户确认分层报告）

用户确认分层报告，无待确认产品问题。实现独立visual包，Build(ctx, ReaderAt, size, Options)复用AnalyzeSpace，WriteHTML输出自包含离线HTML；CLI新增visualize。默认仅空间层，不自动尝试行解码。--index精确选择一棵普通聚簇或二级索引，完整扫描该索引以生成真实父子边；--pages选择原始页字节及这些页上的记录/LOB来源钻取。无--index时仍能查看所选页的原始字节和空间信息，但不声称已解码字段。VIRTUAL聚簇视图仍需--materialized。首版单文件，暂不加入分区清单报告；空间层可分析分区物理文件，单文件索引发现原拒绝契约保持。

只保留所选页的记录位置、系统/删除/行版本元信息和LOB来源，不将用户值及整段LOB嵌入HTML；所选索引仍完整解析/校验以保证树边和路径真实。没有字节嵌入的页可看统计，但界面明确提示重新用--pages加入，不在线读取原文件。普通列逐字段起止位置未由当前Record API提供，不猜测；支持记录边界、外部引用字段和LOB块的精准定位。图与页详情区分树父子边、同层链和LOB逻辑顺序。

沿用SpaceOptions和ScanOptions，外层累计源读取预算覆盖空间、索引发现/扫描及选定原始页；扫描内部仍独立计算请求/缓存预算。空间层默认最多100000页，扫描默认预算沿用原值。最多显式256原始页，最多10000条选定记录详情，默认嵌入报告JSON上限64MiB；超过预算返回ErrLimit，禁止悄悄抽样或截断。该上限不是进程RSS上限；DOM按页和记录分页渲染，不能为全部页同时生成大量控件。

所有请求解析完成后才渲染；Build失败返回nil，HTML编码/写入失败返回错误。CLI复用同目录临时文件/原子发布、输入身份保护、context和退出码。HTML内全部JSON数字转换为精确十进制字符串；LSN/ID比较用BigInt或字符串，不能经JS浮点。原始JSON使用encoding/json转义，再作为脚本数据嵌入，动态文本只用textContent，不执行文件内名称。无CDN/网络/服务进程，页面提供搜索、树/页/记录/LOB导航、十六进制范围高亮及本地手册链接；链接由报告可选的本地手册目录生成，未指定时显示章节名称和仓库相对位置。

验收沿用准备条目，增加默认空间可成功而显式不支持索引失败、只嵌入所选页、完整树边与API对照、无误导的缺字节状态、恶意名称HTML转义和64位极值测试。固定样本可复用，交付手册81–82与浏览器实际交互验证。

第39阶段来源细化：ExternalField完整保留公开API的来源结构，包括COMPACT最多768字节的本地Prefix及20字节Reference；这些字节本来就在显式所选页内。不得把Prefix清空后误称完整物理来源；不嵌入的是完整Values及页外LOB载荷。

第39阶段浏览器验收补充：内置浏览器拒绝file://且禁止间接绕过，未更换表面或启动HTTP绕行。向用户交付固定二级树/LOB报告后，用户明确回复“已检查，交互正常”，覆盖提问中的页号定位、父子页、记录高亮及LOB块导航。将此记录为用户本地浏览器验收，而非工具自动视觉测试；数据/字节/HTML与脚本语法由自动检查独立验证。

## 2026-09-30：第39阶段验收关闭

保持用户已确认的分层与严格失败契约。中断回归不作为通过证据，用户浏览器反馈与自动验收分别记录；重新执行和收尾结果见[阶段 39 历史验收](../HISTORY.md#stage-39)。

## 2026-09-30：第40阶段首版冻结与收尾

- 问题：把第1–39阶段已验收能力整合为可判断输入范围、可复验、可交付的首版基线，消除入口文档中早期阶段的过期限制。用户已授权下一阶段及收尾。
- 采用最小方案：冻结现有解析器与七个CLI命令，交付支持矩阵、API/失败/资源契约索引、故障复现和验证入口；以2026-09-30标识本地首版基线，不自行引入SemVer稳定性承诺或远程发布。可选扩展继续未实施。
- 已检查go.mod（Go1.25.0、无第三方依赖）、现有全夹具/SQL预期/官方SDI证据、race/vet/fuzz、扫描基准和CLI子进程测试。Go工具链与Python标准库argparse/pathlib/subprocess/hashlib/json足够完成复验归档；复用既有测试，避免新增解析逻辑和重复矩阵测试。graphify仅提供Java参考节点，不据此声称Go能力。
- 排除为收尾扩大MySQL版本/页大小/类型支持、生成新数据库、重新采集已具备独立证据的全部夹具、重做发布框架或默认联网发布。性能实验只作本机基线，不承诺RSS或跨平台性能。
- 验收：交付手册83–84及统一首版入口；当前支持/明确拒绝/未验证分开，真实组合与合成拒绝分开；既有单表、空间与分区基线保持，具体计数见[阶段 40 历史验收](../HISTORY.md#stage-40)。复验入口记录输入指纹、工具环境、完整race/vet、关键主动fuzz、基准与成功/失败CLI烟测，失败必须非零且不形成成功状态。已有官方工具与SQL采集证据引用原始记录，不冒称本阶段重新运行MySQL。修正README已过期声明，检查本地链接，更新所有状态文件后关闭阶段。
- 当前无影响范围或契约的待确认问题；本地交付足以满足本次收尾，不创建远程Release、标签或安装包。

## 2026-09-30：第40阶段验收关闭

验收正文已合并至[历史验收记录](../HISTORY.md#stage-40)。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-40)。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-40)。

验收正文已合并至[历史验收记录](../HISTORY.md#stage-40)。

## 2026-09-30：Java参考引用迁移到GitHub

用户要求将本地Java参考引用改为GitHub链接，为移除参考目录作准备。仅迁移项目文档中的参考入口和具体源码位置，保留历史调研事实；链接固定至1.0.10标签对应提交8f8dad1aa16439a116f07f544e55c96501032b9a，避免主分支变化造成行号漂移。校验远程文件树、引用文件及行号；不删除Java目录、不改Go源码/夹具，不重写graphify历史索引的内部路径。

验收正文已合并至[历史验收记录](../HISTORY.md#task-52)。

## 2026-10-08：命令行使用指南

- 问题：让使用者能够查阅当前正式命令行功能并直接复现元数据、导出、查询、检查、空间分析和可视化操作。用户要求为已实现的工具编写文档并添加使用示例。
- 决定：新增中文 `docs/CLI.md`，从 README 和解析手册目录提供入口；覆盖七个命令、全部公开参数、查询 JSON、分区清单、无损格式、完成/失败和资源边界。当前支持范围沿用首版第83章，不改变解析器或协议。
- 已检查 Go 标准库 `flag`、`encoding/json`、`encoding/csv`、`go.mod` 和 `internal/cli`，以及手册77–84章与现有夹具；文档与验证复用现有 CLI 和 Python 标准库，不增加依赖。graphify 查询词来自现有词汇，但图谱只覆盖 Java 参考，Go 行为以直接源码和命令实跑为准。
- 更简单的方案是独立使用指南配合现有原理手册。未采用将全部操作说明追加到 README：入口会过长；未采用重写77–84章或新增命令：本次需要使用文档，已有原理与协议说明可以链接复用。
- 验收：每个命令有用途、语法、参数及可复现示例；核对各命令帮助，使用固定夹具执行示例并检查输出和退出状态；校验新增本地链接，确认仅文档变化。示意协议片段明确标注，真实预期来自固定夹具；不连接 MySQL 或修改原始资产。

验收正文已合并至[历史验收记录](../HISTORY.md#task-53)。

## 2026-10-08：初始化Git并提交至用户仓库

- 用户明确授权初始化当前项目并推送至 `https://github.com/kimihiro-dev/innodb-go-reader.git`。已检查本地无Git仓库，远端引用为空；采用 `main` 分支和 `origin` 远端，首次提交包含源码、文档、示例、脚本及固定测试资产。
- 新增最小 `.gitignore`，排除macOS系统文件、Python字节码/缓存、graphify本地导航产物和根目录构建二进制；不删除这些本地文件。最大项目文件约2.3MiB，无需Git LFS；不忽略回归夹具或交付HTML示例。
- 本机没有既有Git作者配置；使用经GitHub公开API核对的 `kimihiro-dev`（用户ID62640180）和 `62640180+kimihiro-dev@users.noreply.github.com`，仅配置当前仓库。认证凭据不写入仓库或文档。
- 复用Git及当前Go工具链，不增加依赖；不重建解析器、不引入许可证或发布标签。未采用覆盖远端历史、强制推送或提交本地缓存；本次目标是保留当前项目的可复现初始版本。
- 验收：构建CLI和现有CLI专项测试通过，核对暂存内容/忽略规则与文件规模；创建本地提交，正常推送并核对远端 `main` 与本地提交哈希一致。若认证不可用，先完成可审阅的本地提交，再请求恢复GitHub认证。

验收正文已合并至[历史验收记录](../HISTORY.md#task-54)。

## 2026-10-08：使用用户新增SSH密钥重试推送

- 用户已将本机GitHub专用SSH公钥添加到账号，并明确要求重试。使用对应专用密钥验证，GitHub成功认证为 `kimihiro-dev`；不更换提交作者或重建已有提交。
- 当前网络关闭github.com的22端口连接，采用[GitHub官方443端口SSH入口](https://docs.github.com/en/authentication/troubleshooting-ssh/using-ssh-over-the-https-port)，origin改为 `ssh://git@ssh.github.com:443/kimihiro-dev/innodb-go-reader.git`。仅在当前仓库配置SSH密钥选择，不修改用户全局Git/SSH配置。
- 主机公钥从GitHub官方HTTPS元数据获取并严格核对，保存于本地 `.git/github_known_hosts`；私钥不复制或提交。保留严格主机检查，正常推送main，不强制覆盖任何远端历史。
- 接续TODO第54项；推送现有提交后核对远端哈希，再提交并同步验收状态文档。

验收正文已合并至[历史验收记录](../HISTORY.md#task-54)。

## 2026-10-08：固定v0.1.0源码并准备手动Release

- 问题：用稳定版本标签标识当前已交付源码，并提供用户手动创建GitHub Release所需的多平台编译命令和发布说明。用户已授权创建/推送源码标签，明确由用户手动添加Release。
- 采用附注标签 `v0.1.0`，固定本轮开始时已推送的 `116de5046aa94b379ce4d2e0711e34be9766c397`。发布说明、构建指引和状态记录作为后续main文档提交，不改变该源码快照；后续修订使用新版本，不移动已推送标签。
- 新增 `docs/releases/v0.1.0.md` 和 `docs/RELEASING.md`，README提供入口。编译命令从标签导出的源码构建macOS/Linux/Windows的amd64和arm64六组二进制，CGO_ENABLED=0、-trimpath；提供tar.gz/zip归档与SHA256SUMS，不将构建产物加入Git。
- 已核对go.mod、现有CLI和首版支持矩阵，现有Git、Go、tar/zip及SHA256工具足够；无新依赖。版本体现在源码标签和资产文件名，不新增--version、构建注入、自动发布工作流或稳定API承诺。
- 未采用给新增文档提交打标签：本次明确固定既有当前源码。未采用自动创建Release或上传资产：用户要求手动完成。未采用仅改文件名后宣称多平台运行支持：必须区分交叉编译与目标平台运行验证。
- 验收：六组构建、归档及校验命令实际通过；本机darwin/arm64样本烟测，其他目标只记录编译证据；发布说明与支持矩阵一致，本地链接有效，源码/夹具未改。正常推送main文档和标签，核对远端标签解引用为上述固定提交；不创建GitHub Release。

归档核对发现macOS tar默认附加AppleDouble文件；打包命令设置COPYFILE_DISABLE=1，去掉这类本机元数据，使每个包仅包含对应程序，重新生成匹配资产的SHA256SUMS。

验收正文已合并至[历史验收记录](../HISTORY.md#task-55)。
