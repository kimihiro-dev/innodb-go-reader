# InnoDB Java Reader 1.0.10 调研与 Go 分阶段建议

调研日期：2026-09-09。状态：已完成的历史调研；本文建议只代表当时提案。当前 Go 范围见 [项目需求](REQUIREMENTS.md)与[支持矩阵](format/83-release-support-matrix.md)。

规划更新：本文的页检查器优先路线为历史建议。用户随后明确数据还原优先并要求逐步讲解文档；当前路线以 [第一阶段计划](STAGE_PLANS.md#stage-1) 和项目需求/决策文档为准，本文源码调研结论继续保留。

<a id="1-结论"></a>

## 结论

参考项目是一个以单表 `.ibd` 为输入、依赖外部表结构的只读页检查和索引记录读取库，并提供 CLI、数据导出及页热力图。它包含真正的 B+ 树点查与范围遍历，不只是逐页扫描；但不是完整 SQL 引擎，也不是实现事务恢复和一致性快照的备份工具。

适合借鉴：页布局、记录头/NULL 位图/变长字段解码、聚簇与二级索引遍历、类型边界测试。

不宜照搬：根页位置推算、简化字符串比较、对未支持 LOB 的默认宽松处理、Java 类层次和占位 I/O 实现。

推荐先做独立的 Go 页检查器，再增加有限类型的行扫描和索引查询。以下阶段均为建议，不代表已批准实施范围。

<a id="2-分析依据与验证程度"></a>

## 分析依据与验证程度

- 主要依据：调研时使用的InnoDB Java Reader 1.0.10源码快照，现以[GitHub固定提交](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a)提供导航。[README](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/README.md)、[根POM第11行](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/pom.xml#L11)及下文源码链接均固定到该版本。
- [上游1.0.10标签](https://github.com/alibaba/innodb-java-reader/tree/1.0.10)已核对为提交`8f8dad1aa16439a116f07f544e55c96501032b9a`（2026-09-30）。本次迁移通过GitHub文件树的Git blob SHA逐一核对链接涉及的文件与调研快照一致；不据此宣称全仓库逐文件一致，也不引用持续变化的主分支行号。
- 已进行静态源码核对和本地 MySQL 只读配置查询。本轮未编译 Java、未运行 Maven 测试、未用 Java 读取 8.0.45 生成的数据文件。
- 核心库 85 个 Java 文件通过 graphify 建立索引：805 个节点、1,848 条边、41 个社区。图用于定位代码，最终能力判断回到源码；工具产生的推断边不作为支持承诺。
- 调研时生成的graphify图与导航报告是可选历史索引，内部保留采集时的本地路径；移除参考目录后请使用本文GitHub链接查看源码。其 token cost 为 AST 提取的 LLM 调用量（0），不是本轮对话的用量。图的结构检查未发现缺失端点或自环，不等于语义关系全部正确。

下文具体源码均链接到固定提交；**C**表示[核心源码目录](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader)，**T**表示[测试源码目录](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/test/java/com/alibaba/innodb/java/reader)。带行号的引用可直接跳转到对应位置，无需保留本地Java目录。

<a id="3-模块划分"></a>

## 模块划分

| 模块 | 职责 | Go 项目可借鉴的部分 |
|---|---|---|
| [innodb-java-reader](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader) | 文件读取、页结构、表结构、字段解码、索引查询 | 主要参考对象 |
| [innodb-java-reader-cli](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader-cli) | 命令行、格式化输出、文件导出 | 用户操作及输出形式，后置于核心解析 |
| [innodb-heatmap](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-heatmap) | LSN 和填充率 HTML 热力图 | 可独立建立在页统计结果之上 |
| [innodb-java-reader-demo](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader-demo) | 示例 | 理解 API 使用场景 |

核心调用关系：

```text
DDL / 显式 TableDef
        ↓
TableReaderFactory / TableReaderImpl
        ├── StorageService → FileChannel → InnerPage → 具体页类型
        └── IndexServiceImpl → B+ 树定位与遍历
                               → 记录布局解码 → ColumnFactory → GenericRecord
                                                        ↓
                                            CLI 导出 / 调用方处理
页头 LSN、INDEX 页空间信息 → 热力图模块
```

`TableDef`、`Column` 描述逻辑结构；`SliceInput` 提供字节读取；`IndexServiceImpl` 同时承担索引算法与记录解码。Go 版本可按职责逐步拆分，但无需预先建立一套与 Java 对应的接口和工厂。

<a id="4-已实现能力"></a>

## 已实现能力

| 能力 | 具体范围 | 主要源码依据 |
|---|---|---|
| 表结构输入 | CREATE TABLE 字符串、含多条 DDL 的 SQL 文件、程序化 TableDef；可按表名创建 reader | C [TableReaderImpl.java:66](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L66)；[schema/TableDefUtil.java:35](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/schema/TableDefUtil.java#L35)；[schema/provider/impl/](https://github.com/alibaba/innodb-java-reader/tree/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/schema/provider/impl)；[TableReaderFactory.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderFactory.java) |
| 页枚举与读取 | 页数、全部页、逐页迭代、全部 FIL 头、指定页 | C [TableReaderImpl.java:104](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L104)、[:120](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L120)、[:140](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L140)、[:150](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L150) |
| 通用页信息 | FIL header/trailer，页号、前后页、LSN、表空间 ID、校验和字段 | C [page/FilHeader.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/FilHeader.java)、[page/FilTrailer.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/FilTrailer.java)、[page/InnerPage.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/InnerPage.java) |
| 具体页解析 | FSP_HDR、XDES、IBUF_BITMAP、INODE、INDEX、ALLOCATED、旧 BLOB；SDI 只有通用包装 | C [TableReaderImpl.java:150](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L150) 的 switch；[page/SdiPage.java:11](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/SdiPage.java#L11) |
| 索引页结构 | INDEX header、层级、记录数、目录槽、infimum/supremum、子页指针、记录链 | C [page/index/Index.java:41](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/index/Index.java#L41)；[page/index/RecordHeader.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/index/RecordHeader.java) |
| 全表读取 | 聚簇索引 B+ 树遍历；列表返回；Predicate 过滤；列投影 | C [service/impl/IndexServiceImpl.java:243](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L243)；[TableReaderImpl.java:224](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L224) |
| 指定页记录 | 读取索引页记录；区分叶子整行与非叶子键/子页信息 | C [service/impl/IndexServiceImpl.java:119](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L119) |
| 主键点查 | 单列/复合键；沿 B+ 树下降，页内先目录二分再沿记录链查找 | C [service/impl/IndexServiceImpl.java:262](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L262)、[:866](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L866) |
| 主键范围查询 | 上下界、开闭区间、无界；支持升降序及列投影 | C [TableReaderImpl.java:244](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L244)、[:300](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L300)；[service/impl/IndexServiceImpl.java:484](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L484) |
| 流式迭代 | 全表、主键范围、二级索引范围迭代，避免 queryAll 的全量列表 | C [TableReaderImpl.java:280](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L280)、[:300](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L300)、[:332](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L332) |
| 二级索引查询 | 指定索引；等值可用相同上下界表达；复合键、范围查询、回聚簇索引取行、满足条件的覆盖索引投影 | C [service/impl/IndexServiceImpl.java:345](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L345)，覆盖分支 [:375](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L375) 附近、回表分支 [:396](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L396) |
| 无显式主键 | 有合适的全非空唯一键时作为聚簇键；否则处理隐藏 6 字节 ROW_ID | C [schema/TableDef.java:135](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/schema/TableDef.java#L135)；[service/impl/IndexServiceImpl.java:1044](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L1044)；T [pk/FirstUniqueKeyAsPrimaryKeyTableReaderTest.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/test/java/com/alibaba/innodb/java/reader/pk/FirstUniqueKeyAsPrimaryKeyTableReaderTest.java)、[pk/NoPrimaryKeyTableReaderTest.java](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/test/java/com/alibaba/innodb/java/reader/pk/NoPrimaryKeyTableReaderTest.java) |
| 记录布局 | NULL 位图、1/2 字节变长长度、字段投影跳读、旧式页外字段链 | C [service/impl/IndexServiceImpl.java:959](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L959)、[:1210](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L1210) |
| 校验 | 可选文件长度检查、页校验，CRC 路径及旧 InnoDB 算法回退 | C [config/ReaderSystemProperty.java:52](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/config/ReaderSystemProperty.java#L52)、[:66](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/config/ReaderSystemProperty.java#L66)、[:72](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/config/ReaderSystemProperty.java#L72)；[page/InnerPage.java:74](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/InnerPage.java#L74) |
| 空间统计 | 单 INDEX 页及全部 INDEX 页填充率 | C [TableReaderImpl.java:180](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/TableReaderImpl.java#L180)；[page/index/Index.java:118](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/page/index/Index.java#L118) |

“支持”在此指存在实现及相应用例线索，不代表本轮已实测所有输入组合。

### 字段类型

[ColumnFactory.java:904](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/column/ColumnFactory.java#L904) 附近的类型注册和各解码器覆盖：

- 整数：TINYINT、SMALLINT、MEDIUMINT、INT/INTEGER、BIGINT，包括 unsigned；内部 ROW_ID。
- 浮点和定点：FLOAT、REAL、DOUBLE、DECIMAL/NUMERIC。
- 字符和二进制：CHAR、VARCHAR、BINARY、VARBINARY，各级 TEXT/BLOB。
- 日期时间：DATE、TIME、DATETIME、TIMESTAMP、YEAR，包含相应小数秒精度处理。
- 其他：BOOL/BOOLEAN、ENUM、SET、BIT。

类型名支持不等于该类型所有存储形式均支持：尤其 TEXT/BLOB/长变长字段落入 MySQL 8.0 新式 LOB 页时存在明确缺口。没有在类型注册中找到 JSON 或 GEOMETRY 解码器。

### CLI 与热力图

[innodb-java-reader-cli/.../cli/CommandType.java:17](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader-cli/src/main/java/com/alibaba/innodb/java/reader/cli/CommandType.java#L17) 定义 10 条命令：

```text
show-all-pages                  show-pages
query-by-page-number            query-by-pk
query-by-sk                     query-all
range-query-by-pk               gen-lsn-heatmap
gen-filling-rate-heatmap        get-all-index-page-filling-rate
```

CLI 支持输出文件、分隔符/引号/表头选项和页信息 JSON 展示。导出是数据行输出，不是包含完整 DDL 与事务语义的 mysqldump 替代品。输出端存在 Buffer/Mmap/Direct writer，不能据此推断读取端也实现了三种 I/O。

LSN 热力图反映页面 LSN 分布；填充率基于页内空间统计。它们不是 MySQL 缓冲池访问频率或实时读写热点监控。

<a id="5-明确限制与兼容性风险"></a>

## 明确限制与兼容性风险

| 边界 | 核查结果 | 对 Go 版本的启示 |
|---|---|---|
| 版本声明 | README 声明 5.6/5.7/8.0；测试资源文档记录 5.6.39、5.7.27、8.0.18 | 8.0.45 必须单独生成样本验证 |
| 文件与格式 | 文档要求 file-per-table、16 KiB、COMPACT/DYNAMIC | 不自动扩展到共享/通用表空间或其他页大小 |
| 压缩/加密/REDUNDANT | 压缩在 Future works；未找到加密页解码；REDUNDANT 枚举不代表记录解码已支持 | 明确支持边界并返回可解释的错误 |
| SDI 元数据 | SdiPage 仅调用 super；没有从 SDI 生成 TableDef 的实现 | “识别 SDI 页”和“恢复表结构”分为两个任务 |
| FRM/系统字典 | MysqlFrmTableDefProvider.load 直接抛 UnsupportedOperationException；系统表空间元数据为后续计划 | 显式 schema/DDL 仍是读取记录的前提 |
| 新 LOB | [IndexServiceImpl.java:931](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L931) 明确 TODO；抛错开关默认 false | 不能把未读出的值当成正确值；首版宜明确拒绝 |
| 根页发现 | 聚簇入口以 ROOT_PAGE_NUMBER=3 为起点并跳过 SDI；二级根页按聚簇根页、索引序号等推算 | 不硬编码为通用规则；后续独立实现根页发现或显式输入 |
| 索引 DDL 历史 | [Workaround.java:25](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/schema/Workaround.java#L25) 注释承认增删索引后推算可能出错；CLI 可显式指定根页/序号 | 测试应覆盖 ALTER 后索引布局 |
| 字符串键比较 | [Utils.java:239](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/util/Utils.java#L239) 使用 compareToIgnoreCase，否则 Comparable.compareTo | 不能视为完整 MySQL collation；先采用整数键降低复杂度 |
| NULL 比较语义 | [Utils.java:230](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/util/Utils.java#L230) 附近 TODO 承认范围查询可能包含 SQL 不应返回的 NULL | 查询边界测试需独立于解码测试 |
| 事务可见性 | readRecord 在 [IndexServiceImpl.java:1058](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/main/java/com/alibaba/innodb/java/reader/service/impl/IndexServiceImpl.java#L1058) 附近直接跳过事务 ID 和 roll pointer 共 13 字节 | 未实现 undo 版本追溯/ReadView；读取磁盘不等于事务一致的 SELECT |
| 日志和恢复 | 公开 API 与实现没有 redo/undo/binlog 解析和恢复链路 | 如未来需要恢复工具，应另立需求 |
| 读取 I/O | FileChannelStorageServiceImpl 实现读取；MmapStorageServiceImpl/DirectIoStorageServiceImpl 的 loadPage 抛未实现异常 | 首版使用普通随机读取足够 |
| 校验默认值 | 文件长度检查、页校验均默认关闭 | “有校验能力”不等于默认检查所有文件 |
| 现代 DDL/索引特性 | 未发现 INSTANT 行版本、降序键编码、函数/多值索引等完整处理证据 | 列为未验证范围，不能由“8.0 支持”推导 |

MySQL 8.0.29 起 INSTANT ADD/DROP COLUMN 引入新的行版本处理，这与本地 8.0.18 测试资源存在实际版本差距。[MySQL 官方 Online DDL 文档](https://dev.mysql.com/doc/refman/8.0/en/innodb-online-ddl-operations.html)

<a id="6-可复用测试资产"></a>

## 可复用测试资产

核心测试目录包括类型边界、超过 8 个 nullable 列、复合/字符串/隐藏主键、全非空唯一键作为聚簇键、多层 B+ 树、范围与升降序迭代、二级索引、删除后读取、旧页外字段、CRC 等用例。资源目录共找到 129 个 `.ibd`/`.sql` 文件（合计，不是 129 个测试场景）。

测试资源版本见 [innodb-java-reader/src/test/resources/testsuite/README.md](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/test/resources/testsuite/README.md)。应借鉴用例意图，并为 Go 重建清晰的生成与断言流程。

特别注意：T [column/ColumnBlobTableReaderTest.java:115](https://github.com/alibaba/innodb-java-reader/blob/8f8dad1aa16439a116f07f544e55c96501032b9a/innodb-java-reader/src/test/java/com/alibaba/innodb/java/reader/column/ColumnBlobTableReaderTest.java#L115) 明示 MySQL 8.0 LOB 未支持，测试包含对应特殊预期。因此即使旧测试全绿，也不能证明新式 LOB 正确。

建议后续两套基准并存：

1. 固定二进制夹具：页/字段已知字节、截断文件、越界页号、损坏字段，适合离线单元测试。
2. 本地 8.0.45 生成夹具：保存 SQL、实际表属性、查询结果、索引根页与快照过程，用于端到端对照。

<a id="7-本地环境实测"></a>

## 本地环境实测

只读查询成功；未创建表、未修改数据、未执行 FLUSH/锁表或停库。

| 项目 | 结果 |
|---|---|
| MySQL | 8.0.45 |
| innodb_page_size | 16384 |
| innodb_file_per_table | 1 |
| innodb_default_row_format | dynamic |
| innodb_checksum_algorithm | crc32 |
| character_set_server | utf8mb4 |
| collation_server | utf8mb4_0900_ai_ci |
| Go 工具链 | go1.25.7 darwin/arm64 |

MySQL 客户端：`/Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql`。
Socket：`/Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock`。
数据目录：`/Users/kimihiro/workspace/data/mysql/3306/data/`。

以上是实例默认配置，不说明所有既有表均为该行格式或字符集，也未验证每个表的加密、压缩或 DDL 历史。查询未返回 `innodb_default_encryption` 项，不据此推断表是否加密。

后续宜使用专用测试表。在适用的独立表空间表上，可在同一会话执行并持有 `FLUSH TABLES ... FOR EXPORT` 的锁，复制文件及记录基准后再 `UNLOCK TABLES`，或使用其他经过验证的一致快照方案；不能让获取锁的客户端先退出再复制。本轮仅提出流程，未执行。[MySQL 官方表空间导出说明](https://dev.mysql.com/doc/refman/8.0/en/innodb-table-import.html)

<a id="8-go-标准库与依赖调查"></a>

## Go 标准库与依赖调查

当前项目没有 `go.mod`、Go 源码或现有 Go 依赖。已检查本机 Go 1.25.7 标准库：

| 需求 | 可用能力 | 仍需实现的部分 |
|---|---|---|
| 随机页读取 | os.File.ReadAt、io.ReaderAt | 页号/偏移验证、完整页读取、错误上下文 |
| 固定字段解码 | encoding/binary.BigEndian/LittleEndian | 按具体 InnoDB 字段布局选择字节序和位操作 |
| 页 CRC | hash/crc32（含 Castagnoli） | 按 InnoDB 规则选择字节区间和组合，不能整页随意计算 |
| CLI/输出/测试 | flag、encoding/json、testing | 少量具体命令和验收夹具 |

检查位置包括本机 `src/os/file.go`、`src/io/io.go`、`src/encoding/binary/binary.go`、`src/hash/crc32/crc32.go`。标准库提供底层构件，没有现成的 InnoDB 页、记录、SDI 或 MySQL DDL 解析器。

Java 核心 POM 依赖 JSqlParser、Guava、Commons、Jackson 等；不应逐个寻找 Go 等价物。首个页检查阶段可以仅用标准库；在需要行解码时，先显式提供最小 schema，比立刻引入完整 MySQL SQL parser 更简单。尚未选择第三方依赖。

<a id="9-分阶段建议及验收目标待确认"></a>

## 分阶段建议及验收目标（待确认）

| 阶段 | 建议范围 | 验收方法 |
|---|---|---|
| P0：页检查器 | 只读独立 .ibd；先限定 8.0.45、16 KiB、非压缩非加密；页数、FIL 头尾、类型统计、INDEX 头、FSP 基本标志、CRC 校验；未知类型仍可展示通用头 | 用已知字节夹具检查偏移/字节序；测试截断/越界/损坏；以 8.0.45 专用表快照核对页数、space/index ID、层级、根页及官方校验工具结果 |
| P1：最小行扫描 | 显式 schema；新建无 INSTANT 历史的简单 DYNAMIC 表；整数主键、INT/BIGINT/VARCHAR、NULL；先页内字段，拒绝页外值；聚簇树叶子流式遍历 | 0 行/单页/多层树；负数与边界值、空串/NULL/UTF-8；逐行与快照对应的 ORDER BY 主键结果比较 |
| P2：主键查询 | 整数主键点查、开闭区间与无界范围，之后复合键 | 覆盖不存在键、边界、跨页、复合键；结果和排序与 MySQL 对照 |
| P3：元数据能力 | 决定 DDL/SDI 优先级；可靠根页定位；再扩大 schema 和类型范围 | 比较 SHOW CREATE TABLE、字典元数据；覆盖索引增删及明确支持的 DDL 历史 |
| P4：二级索引 | 整数二级键先行；重复键、回表、覆盖索引；字符串 collation 单独验收 | 与对应 SQL 对照，测试 NULL、重复值、复合索引及边界 |
| P5：按需求选择 | 新式 LOB、JSON、INSTANT 行版本、其他页大小、压缩/加密、热力图、性能优化 | 每项独立建立支持矩阵和实测夹具，不捆绑承诺 |

P0 的问题陈述：让用户可靠地看清一个受支持的 `.ibd` 文件由哪些页组成，并定位基础结构或校验异常。

P0 暂不需要表结构恢复、行解码、完整 DDL、mmap、direct I/O、插件架构或并发缓存。先使用 `ReadAt` 和小型解码函数；性能优化等有基准后再决定。

尚待用户确认的事项：

1. 首阶段选页检查器，还是优先得到有限类型的行导出？推荐前者。
2. 首版是否接受先限定本地 8.0.45、16 KiB、独立非压缩非加密表空间？推荐接受，后续逐项扩展。
3. 行读取阶段的元数据优先显式 schema、DDL 还是 SDI？不阻塞本轮调研，可在 P1 之前确定。

最终是否需要恢复、日志解析、完整类型覆盖或可视化，保留为未来需求讨论，不作为当前非目标永久排除。
