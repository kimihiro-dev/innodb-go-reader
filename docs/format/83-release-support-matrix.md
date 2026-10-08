# 83. 首版支持矩阵与入口选择

## 基线与判断方法

本章冻结2026-09-30首版的能力范围；[第84章](84-release-validation.md)给出构建、验收与故障复现。旧章节保留各阶段的教学上下文，其中“当前不支持”可能已由后续阶段扩展，当前承诺以本章、实际API和最新需求为准。

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

## 列值、字符集与索引

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
| 元数据 | ReadSDI、InspectTable、InspectSecondary / metadata | 对象、schema及入口报告；Issues不能忽略 |
| 完整当前行 | Read/ReadAuto及Materialized变体 / export、check rows | 受支持聚簇树及请求的当前值；Read失败不返回部分结果 |
| 逐行消费 | Scan及Auto/Materialized变体 | 回调可能已有前缀，必须同时检查error和Complete |
| 聚簇点查/范围 | Query及Auto/Materialized变体 / query | 索引顺序边界、前导列Prefix、Reverse、Limit；不认证未访问子树 |
| 二级物理项 | ReadSecondary、ScanSecondary及Auto变体 / check secondary | 独立二级树，非完整用户行，不自动回表 |
| 二级投影 | QuerySecondary及Auto/Materialized变体 / query | 覆盖/按需回表；未选LOB不读取，预算跨路径累计 |
| 完整分区集合 | InspectPartitions、ScanPartitions及Materialized / --manifest | 预检全部文件、定义序扫描；后段失败仍可已有输出前缀 |
| 空间 | AnalyzeSpace / space、check space、page | 分配/使用中页/局部结构；不等同键顺序或完整字段校验 |
| 离线图 | visual.Build、WriteHTML / visualize | 默认空间；显式选索引完整扫描，所选页嵌入详情；请求层失败不降级 |
| 原始大LOB | StreamLOB | 按引用与预算的当前原始字节流，不解释为SQL值 |

`ErrUnsupported`表示未支持输入，`ErrCorrupt`表示可检测矛盾，`ErrLimit`表示预算，`ErrStopped`表示回调主动停止；使用`errors.Is`分类，并保留底层I/O/context错误。显式schema必须可信，解析器不能发现所有逻辑定义错配。ReaderAt稳定性、打开/关闭与回调保留数据由调用方负责，取消不保证打断任意底层阻塞I/O。

CLI退出0/2/1/130分别代表成功/参数错误/运行失败/取消；stdout为数据，stderr为诊断。JSONL必须读到end并核对行数和尾随EOF；CSV采用带类型单元格协议，EOF不证明生产成功。文件输出仅成功后原子发布，默认不覆盖；显式overwrite仍保护输入身份。stdout失败前缀不可撤回，目录断电持久性不作承诺。

## 预算与内存

| 层级 | 默认值/固定限制 | 计量说明 |
|---|---|---|
| Scan/Query | 缓存64页；页请求/遍历项/行各1000000；单行源字节64MiB | 命中缓存仍计页请求；二级回表和分区共享累计预算 |
| StreamLOB | MaxLOBBytes 64MiB | 可显式配置；类型物化16MiB上限保持 |
| SDI | 单对象16MiB、累计解压64MiB、4096对象 | 独立容量限制 |
| 分区集合 | 最多1024文件、集合SDI JSON64MiB；CLI清单1MiB | 清单相对路径以清单目录为准 |
| AnalyzeSpace | 1000000页、10000000遍历项 | 不物化用户行 |
| visualize | 默认100000页、实际ReadAt累计1000000次、详情10000条、嵌入JSON64MiB；最多256个原始页 | 显式索引另沿用扫描预算；界面分页不代表抽样 |
| rowio读回 | 每条记录128MiB | 字节协议限额，不是进程内存限额 |

这些限制均不是RSS硬上限。Read保留整表结果；Scan按行交付，但调用方保留记录仍会增长内存。性能/内存实验及其限制见[第84章](84-release-validation.md)。
