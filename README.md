# InnoDB Go Reader

用 Go 逐步实现 InnoDB 物理文件解析，并同步编写可跟随实践的中文解析手册。

已实现：离线扫描多页、多层聚簇索引树的所有用户行，支持 TINYINT/SMALLINT/MEDIUMINT/INT/BIGINT（含 UNSIGNED）、多字符集 VARCHAR、VARBINARY、八种 TEXT/BLOB 类型（含受支持的页外 LOB）、DECIMAL 精确小数、FLOAT/DOUBLE、DATE/YEAR、DATETIME(0..6)、TIME(0..6)、TIMESTAMP(0..6)、BIT(1..64)、BINARY(0..255)、ENUM、SET、多字符集 CHAR(0..255)、二进制 JSON、二维 GEOMETRY 家族和 NULL。提供独立 SDI 提取、InspectTable 元数据报告及 ReadAuto 自动读行入口。保留页号、记录偏移、记录头、事务字段及非叶子导航信息。MySQL 8.0.45 的 487 组真实夹具（按快照统计，484 组在对应完整/仅物化入口共恢复 98811 行，另 3 组完整读取明确拒绝）随项目交付，包含 12000 行的三层树，无第三方 Go 依赖。

## 从这里开始

版本源码固定为 [`v0.1.0`](https://github.com/kimihiro-dev/innodb-go-reader/tree/v0.1.0)；[发布说明](docs/releases/v0.1.0.md) · [多平台编译与手动发布](docs/RELEASING.md)。

使用命令行工具请先读 [命令行使用指南](docs/CLI.md)：七个命令的功能、参数、可复制示例、查询/分区清单、导出格式与失败处理。

首版基线（2026-09-30）已冻结：[支持矩阵与API契约](docs/format/83-release-support-matrix.md) · [构建、复验与故障定位](docs/format/84-release-validation.md)。

先读 [解析手册](docs/format/README.md)，再运行：

```sh
go test ./...
go run ./examples/read testdata/mysql8045/lesson_rows.ibd testdata/mysql8045/lesson_rows.json
```

输出为按主键排列、按 schema 列顺序表示的 JSON 行：

```json
[-3,42,"InnoDB"]
[2,null,null]
[7,0,""]
[12,-5,"你好"]
```

Go 1.25 或更高版本。入口为 `innodb.Read(io.ReaderAt, fileSize, Schema)`，调用方负责打开/关闭文件；返回值包含根 `Page`、访问的 `Pages`、导航 `Nodes` 和用户 `Records`。Read结果保存在内存中；需要逐行消费时使用Scan/ScanAuto，显式仅物化视图使用ScanMaterialized/ScanMaterializedAuto。流式回调的已输出前缀不可撤回，必须检查最终Complete与error。示例见 `examples/read/main.go`，跨页读取步骤见[第 08 章](docs/format/08-tree-scan.md)。

## 正式命令行与无损导出

```sh
go build -o /tmp/innodb-reader ./cmd/innodb-reader
/tmp/innodb-reader metadata testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader export --format jsonl --physical testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader check --scope rows testdata/mysql8045/lesson_rows.ibd
```

提供metadata/page/export/query/check/space/visualize；JSONL及CSV都采用带版本、列定义与类型单元格的无损协议，公开`rowio`包可读回原Go值。CSV不是普通文本表格。`--output`完整成功后原子发布，默认不覆盖；诊断/完成摘要走stderr，失败退出非零。JSONL必须核对end，CSV完整性依赖退出状态或原子文件；VIRTUAL仍须显式`--materialized`。详见[第77章](docs/format/77-cli-and-lossless-export.md)和[第78章](docs/format/78-cli-validation-and-failure.md)。

分区表使用完整文件清单：`innodb-reader metadata --manifest files.json`、`export --manifest files.json --output rows.jsonl`、`check --manifest files.json`。先核对所有分区，再按分区定义序、各分区内部聚簇键序读取；每行保留分区来源。缺文件会失败，不承诺全局键序。Go入口为`InspectPartitions`、`ScanPartitions`及`ScanPartitionsMaterialized`；RANGE/LIST/HASH/KEY普通独立分区已验证，子分区和集合查询暂不支持。清单示例、真实布局和边界见[第79章](docs/format/79-partition-collection-layout.md)和[第80章](docs/format/80-partition-validation-and-cli.md)。

离线学习报告：`innodb-reader visualize --output space.html table.ibd`；显式树和字节钻取：`visualize --index PRIMARY --pages 0,4 --output detail.html table.ibd`。默认严格空间层，选定索引完整扫描，只嵌入选定页的字节/记录/LOB来源；任一请求层失败不发布。HTML自包含，LSN不是访问热点。可直接打开[二级树示例](examples/visual/secondary.html)与[LOB示例](examples/visual/lob.html)，用法及资源边界见[第81章](docs/format/81-visual-report-model.md)–[82章](docs/format/82-visual-report-validation.md)。

## 支持边界

- MySQL 8.0.45、16 KiB、独立非压缩非加密表空间、DYNAMIC/COMPACT 行格式。
- 目标写事务已结束的受控稳定快照，支持已验收的页内 UPDATE/DELETE；支持已验收的原生INSTANT ADD/DROP行版本，支持STORED生成列和用户不可见列；VIRTUAL通过显式仅物化入口单独报告，支持已验收的更新后非压缩 LOB 当前值。允许普通BTREE二级索引共存，并通过独立Secondary接口解析受支持二级树。显式非空完整主键（1..16列、最大3072字节，COMPACT每成员最多767字节），支持整数、二进制、四个 `_bin` 字符规则、DECIMAL/BIT/日期时间及每成员 ASC/DESC。
- 支持单叶子及多层树；显式Read需提供可信的列定义、根页号、space/index ID，ReadAuto可从已支持的SDI自动发现。验证层级、父子键范围、同层链与重复页；支持页分裂产生的 free 记录，但不返回它们。
- 值为对应宽度的 Go 有符号/无符号整数、`float32`/`float64`、`string`、`[]byte`、`JSONValue`、`GeometryValue` 或 `nil`，不会将不支持的数据静默替换为 NULL。VARCHAR 声明支持 0..floor(65535/字符最大宽度) 字符，VARBINARY 声明支持 0..65535 字节；识别一/两字节长度，支持页内值及已验收的当前非压缩 LOB。声明上限不保证任意列组合能建表。
- VARBINARY 使用 `max_bytes`，返回独立字节切片；JSON 编码为 Base64，空切片与 SQL NULL 分开。真实字节与示例见[第 11–12 章](docs/format/11-variable-lengths.md)。
- BIGINT UNSIGNED 支持完整 uint64 范围；JSON 输出保留整数数字，消费端需使用 `UseNumber` 或等效方法避免浮点精度丢失。返回类型及真实字节见[第 09 章](docs/format/09-integer-values.md)。`NodePointer.Key` 单列为对应类型值，多列为按主键顺序的 `[]any`，各成员保持原类型。
- 页外支持 MySQL 8.0.45 的 LOB_FIRST/LOB_DATA、LOB_INDEX 外部索引页及已验收的当前版本，支持旧 BLOB 链。DYNAMIC 本地为 20 字节引用，COMPACT 为 768 字节前缀加引用，按实际首个页类型选择新旧链。`Record.External` 保留引用和块来源；`Result.Pages` 仍只包含聚簇页。详见[第 13–14 章](docs/format/13-lob-reference-and-pages.md)。
- TEXT/BLOB 家族支持 TINY/普通/MEDIUM/LONG 四种容量，文本支持 utf8mb4/utf8mb3/ascii/MySQL latin1；无需填写长度属性。单值实现上限 16 MiB，超限明确拒绝；LONG 不代表支持完整 4 GiB。详见[第 15–16 章](docs/format/15-text-blob-types.md)。`LOBChunk.IndexPage` 和 `IndexOffset` 共同定位索引项。
- DECIMAL 支持 precision=1..65、scale=0..min(precision,30)、UNSIGNED/NULL；返回保留 scale 的字符串，避免浮点精度丢失。DECIMAL也可作受支持的索引键；详见[第 17–18 章](docs/format/17-decimal-encoding.md)。
- FLOAT/DOUBLE 以小端 IEEE 754 还原为 float32/float64，支持有限值、次正规数、正负零、NULL/UNSIGNED。JSON 数字应按目标列宽度读取；详见[第 19–20 章](docs/format/19-floating-point-encoding.md)。
- DATE 返回 YYYY-MM-DD 字符串，保留零/部分零及 SQL 模式允许的非日历日期；YEAR 返回 uint16（0 或 1901..2155），NULL 独立。详见[第 21–22 章](docs/format/21-date-year-encoding.md)。
- DATETIME 支持 fsp=0..6，返回保留声明小数位的日期时间字符串，不做时区转换或日历归一化。仅支持当前 DATETIME2 物理格式；详见[第 23–24 章](docs/format/23-datetime-encoding.md)。
- TIME 支持 fsp=0..6、正负时长、超过 24 小时和 NULL，返回保留精度的字符串；范围为 ±838:59:59，端点不能带非零小数。详见[第 25–26 章](docs/format/25-time-encoding.md)。
- TIMESTAMP 支持 fsp=0..6，返回固定 UTC 字符串，保留 NULL、MySQL 零值和小数尾零；不推断原会话时区。详见[第 27–28 章](docs/format/27-timestamp-encoding.md)。
- BIT 支持 bit_length=1..64，返回 uint64，NULL 与零独立，验证未使用高位；JSON 消费端需保留整数精度。详见[第 29–30 章](docs/format/29-bit-encoding.md)。
- BINARY 支持 max_bytes=0..255，返回独立 []byte，保留补零及任意字节；正宽度不消费变长长度数组，零宽度消费一个零长度字节。详见[第 31–32 章](docs/format/31-binary-encoding.md)。
- ENUM 使用有序 enum_values（1..65535 项），Values 返回标签字符串，Record.EnumIndexes 按逻辑列下标保留非 NULL 序号，区分零号与合法空标签。示例只打印标签；详见[第 33–34 章](docs/format/33-enum-encoding.md)。
- SET 使用有序 set_values（1..64 项），Values 返回 SQL 风格字符串，Record.SetMasks 按逻辑列下标保留非 NULL uint64 掩码，区分空集合/空标签选择/NULL。详见[第 35–36 章](docs/format/35-set-encoding.md)。
- CHAR 使用 max_chars=0..255；单字节正宽度为定长，UTF-8和零宽度走变长元数据。Values 去除末尾 ASCII 空格，Record.CharStorage 保留解码后的物理文本及残留空格，TextBytes 保留原始字节。详见[第 37–38 章](docs/format/37-char-encoding.md)。
- Read类完整读取对可检测的不支持布局或损坏返回错误，不返回部分结果；Scan/Query类流式入口可能已交付前缀，必须检查最终状态。
- ReadSDI 可从页0定位SDI根、读取树及SDI_BLOB链并解压原始JSON；保持物理来源，单对象16 MiB、累计64 MiB、最多4096对象。示例 `go run ./examples/sdi table.ibd`，详见[第41–42章](docs/format/41-sdi-records.md)。InspectTable可为受支持表生成schema并核对实际索引根，ReadAuto无需外部schema读取；自动映射暂限已验证SDI版本及[字符集映射表](docs/format/49-charset-storage.md)中的九个文本collation，完整入口遇VIRTUAL明确拒绝；存在函数索引内部隐藏列、旧式INSTANT或升级混合等未支持布局时两种入口均拒绝。显式Read仍要求可信schema；未实现压缩LOB、历史值读取及上述范围以外的类型、事务可见性或恢复；旧BLOB链支持范围见第65–66章。记录中的事务字段仅作原始元信息。
- 读取路径默认严格验证 CRC32C 及头尾 LSN 低32位，覆盖实际访问的表空间头、索引页和 LOB 页；不扫描未访问页，不自动兼容旧/禁用校验算法。详见[第 39–40 章](docs/format/39-page-checksum.md)。

[阶段实施计划](docs/STAGE_PLANS.md) · [需求](docs/REQUIREMENTS.md) · [进度](docs/TODO.md) · [Java 参考分析](docs/REFERENCE_ANALYSIS.md)

自动读取：`go run ./examples/auto table.ibd`；查看元数据与不支持原因：`go run ./examples/auto -metadata table.ibd`。详见[第43章：SDI到schema](docs/format/43-sdi-schema.md)与[第44章：索引根验证](docs/format/44-metadata-roots-validation.md)。第二十阶段已完成：原158资产对照及新增五个真实索引生命周期快照均通过验收，包含页号复用时index ID变化的核对；测试默认离线运行。

JSON列返回保留类型和精确值的JSONValue树，SQL NULL与JSON null分开；JSON()方法生成普通JSON视图，示例 `go run ./examples/json table.ibd`。支持小/大容器、标量、页外值及opaque扩展（未知扩展保留原类型/字节）；暂限单文档16MiB、深度100、100000节点，支持部分更新空洞及无重叠载荷重排。详见[第45章](docs/format/45-binary-json.md)与[第46章](docs/format/46-json-opaque-validation.md)。

空间列返回 GeometryValue，保留 SRID、独立 WKB 和七种二维几何的坐标/环/集合层次；支持页内及既有 LOB 路径、具体类型与可选 SRID 声明。保持存储轴序，不执行坐标转换或拓扑运算；16MiB、100层、100000节点、1000000坐标对上限。详见[第47章](docs/format/47-geometry-storage.md)与[第48章](docs/format/48-geometry-validation.md)。

字符列支持可选 `charset`（默认 utf8mb4；另有 utf8mb3/utf8、ascii、MySQL latin1），Values 返回 UTF-8 字符串，Record.TextBytes 按列下标保留非 NULL CHAR/VARCHAR/TEXT 原字节。支持四类零宽字段及相应字典列编码；详见[第49章](docs/format/49-charset-storage.md)与[第50章](docs/format/50-charset-validation.md)。

复合整数主键用 `primary_keys:["a","b","c"]` 按索引顺序声明，与旧单列 `primary_key` 互斥；Values 仍按SQL列声明顺序返回。自动入口支持完整元组及多层范围校验，包含16列键真实三层树验收。详见[第51章](docs/format/51-composite-key-layout.md)与[第52章](docs/format/52-composite-key-validation.md)。

字符主键须显式指定 `collation`（utf8mb4_bin、utf8mb3_bin、ascii_bin、latin1_bin）；每个键成员可用 `descending:true`。按原编码和 PAD SPACE 比较，其他 collation、聚簇前缀键仍明确拒绝。144 份新增真实文件覆盖双向多类型键及变长非叶子记录，详见[第53章](docs/format/53-typed-key-layout.md)与[第54章](docs/format/54-key-order-validation.md)。

无显式主键时，支持实际唯一非空聚簇索引与隐藏六字节 `DB_ROW_ID`；使用 `clustered_key` 描述，与旧主键字段互斥。`Record.RowID` 保留隐藏身份，`Values` 仍只含用户列。`InspectTable` 区分聚簇/二级入口，二级记录通过独立Secondary接口解析。详见[第55章](docs/format/55-clustered-identity.md)与[第56章](docs/format/56-rowid-layout-validation.md)。

UPDATE/DELETE 后，`Records` 返回未标记删除的物理行，`DeletedRecords` 独立保留删除键、位置、事务字段和本地原始字节，不恢复历史值或追读删除引用。`go run ./examples/changes table.ibd` 可查看行数、delete-mark、页数和垃圾统计。详见[第57章](docs/format/57-delete-mark-layout.md)与[第58章](docs/format/58-change-snapshot-validation.md)。

更新后的LOB按当前活动链还原，允许继承引用、混合块版本和历史索引项共存，保留Chunks来源；旧版本引用及修改进行中仍拒绝。17份新增真实更新快照54行通过SQL/官方工具对照，详见[第59章](docs/format/59-updated-lob-layout.md)与[第60章](docs/format/60-lob-update-validation.md)。

支持MySQL8.0.45原生INSTANT ADD/DROP：`Schema.Instant`描述字段物理位置/生存版本/ADD默认值；`Record.RowVersion`和`DefaultColumns`区分记录版本与补值来源，`Values`仍按当前SQL列序。已验收多轮增删、同名重增、默认值、隐藏键、多页树、版本64及重建，详见[第61章](docs/format/61-instant-row-layout.md)与[第62章](docs/format/62-instant-validation.md)。

含VIRTUAL的表使用 `ReadMaterializedAuto`（或显式schema的 `ReadMaterialized`）：返回Columns、VirtualColumns和Result，Values及列号元信息都对应Columns，VIRTUAL没有占位NULL。普通Read/ReadAuto仍要求完整用户列；INVISIBLE物化列包含在结果中，STORED直接读取已保存值。示例 `go run ./examples/materialized table.ibd`，详见[第63章](docs/format/63-generated-column-layout.md)与[第64章](docs/format/64-generated-validation.md)。

第31阶段新增44份配对快照7732行。普通8.0.45 COMPACT实测使用新LOB；旧BLOB夹具由隔离的官方8.0.45-debug会话调试路径写出，不代表旧版本兼容。布局、真实字节及验收见[第65章](docs/format/65-compact-row-layout.md)和[第66章](docs/format/66-compact-lob-validation.md)。

第32阶段提供同步事件扫描、context取消、有界LRU页缓存及页请求/遍历项/行/源字节预算；独立StreamLOB输出原始字节块，可读取超过16MiB的可信LOB引用，完整类型物化仍保留原上限。不会自动关闭ReaderAt，也不能中断任意阻塞底层I/O。详见[第67章：流式协议](docs/format/67-streaming-contract.md)和[第68章：LOB与资源预算](docs/format/68-streaming-lob-and-budgets.md)。

```sh
go run ./examples/scan testdata/mysql8045/lesson_rows.ibd
python3 scripts/verify_streaming.py
INNODB_SCAN_MEMORY=1 go test -run '^TestScanMemory$' -v
go test -run '^$' -bench '^BenchmarkTableScan$' -benchtime=3x -benchmem
```

Scan示例逐行输出事件，最后输出完成/错误报告；发生中途错误时可能已有stdout前缀。12000行真实样本的GC后采样存活堆约从Read保留结果的59MB降至Scan的1.2MB；不包括输入文件和调用方保留数据，不是进程RSS硬上限或固定性能保证。

第33阶段提供 `Query/QueryAuto` 及显式仅物化入口，支持聚簇键点查、开闭/无界范围、完整前导列前缀、逆序和Limit。上下界按索引ASC/DESC顺序，Reverse只反转输出；通过树导航剪枝，12000行三层树点查仅访问3个聚簇页（完整读取1718页）。查询Complete仅表示请求范围/limit完成，不认证未访问子树。用法见[查询示例](examples/query/main.go)、[第69章](docs/format/69-key-query-navigation.md)和[第70章](docs/format/70-query-validation.md)。

第34阶段提供 `InspectSecondary`、`ReadSecondary/ReadSecondaryAuto` 和 `ScanSecondary/ScanSecondaryAuto`，返回独立二级物理记录、前缀标记、完整聚簇定位键及删除标记。支持普通BTREE唯一/非唯一、NULL、混合ASC/DESC和受支持字符/二进制前缀，明确区分SDI字段与引擎实际追加字段。20棵真实二级树8150当前项、60删除项通过验收；不自动回表或恢复MVCC。详见[二级示例](examples/secondary/main.go)、[第71章](docs/format/71-secondary-record-layout.md)和[第72章](docs/format/72-secondary-validation.md)。

第35阶段提供 `QuerySecondary/QuerySecondaryAuto` 及两个显式 Materialized 入口，支持完整原列范围、前缀候选复核、覆盖投影和按需回表。结果以独立 ProjectedRow 返回选中列与来源，未选 LOB 不读取，所有路径共享缓存与预算。358条独立SQL查询27340行及既有20棵二级树342次查询通过验收。详见[查询示例](examples/secondaryquery/main.go)、[第73章](docs/format/73-secondary-query-navigation.md)和[第74章](docs/format/74-projection-and-lookup-validation.md)。

第36阶段新增 `AnalyzeSpace` 严格空间分析：独立解析FSP/XDES/INODE/IBUF_BITMAP，核对分配链、位图、段和索引归属，分别报告使用中/空闲/未初始化页及页内heap、garbage、目录和连续空间。未知使用中页保留原始信息，错误不返回部分报告。487份既有资产和4份专用空间快照通过专项验收，包含17408页跨XDES组及删除后的空闲残留；原逐行读取基线保持。详见[空间示例](examples/space/main.go)、[手册75](docs/format/75-space-allocation-layout.md)和[76](docs/format/76-space-analysis-validation.md)。
