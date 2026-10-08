# 84. 首版交付、复验与故障定位

## 交付物与构建

2026-09-30本地首版基线包含Go源码、七命令CLI、rowio/visual包、固定夹具、验证脚本和01–84章手册。范围见[第83章](83-release-support-matrix.md)。这是仓库内的能力与证据冻结，没有自行创建远程Release、语义版本标签或跨平台安装包；Go API兼容性不因这次文档冻结被提升为永久保证。

Go模块要求1.25.0或更高版本，无第三方Go依赖。复验脚本另需Python3；HTML专项脚本需要Node，产品自身不需要Python、Node或数据库服务。CLI接收解压后的稳定物理文件，不直接读取`.ibd.gz`。在仓库根目录：

```sh
go build -o /tmp/innodb-reader ./cmd/innodb-reader
/tmp/innodb-reader metadata testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader check --scope rows testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader export --format jsonl --output /tmp/lesson-release.jsonl testdata/mysql8045/lesson_rows.ibd
```

首次执行使用未存在的输出路径。要替换现有输出须明确加`--overwrite`。采用用户已维护的稳定快照采集流程；对在线文件直接复制不能自动证明事务一致性，CRC通过也不能代替该证明。

## 可复跑验收

```sh
python3 scripts/verify_release.py --out /tmp/innodb-release-verification
```

[verify_release.py](../../scripts/verify_release.py)从脚本所在仓库运行，输出目录必须不存在。它依次执行：工具环境记录、完整race/coverage（25分钟超时）、vet、Read/分区/rowio/HTML四项各10秒主动fuzz、3次扫描基准、GC后存活堆实验、CLI构建与成功/拒绝烟测。普通完整Go测试也运行其余fuzz种子；本次主动fuzz不宣称覆盖每个fuzz目标的随机空间。

`report.json`只在全部检查成功且源码/夹具指纹前后一致时标记`complete:true`，检查失败退出非零并保留日志；进程被强制终止时可能没有最终报告，不能认定成功。原始快照只读，损坏实验和CLI输出位于临时目录，无MySQL连接。允许Go测试缓存，日志中的`(cached)`必须照实解释；需要独立新执行时可按下面命令加`-count=1`，不要因首版文档修改无意义地重复长回归。

```sh
go test -race -cover -timeout=25m -count=1 ./...
go vet ./...
go test -run '^$' -bench '^BenchmarkTableScan$' -benchtime=3x -benchmem
INNODB_SCAN_MEMORY=1 go test -run '^TestScanMemory$' -count=1 -v
```

指纹算法：相对路径按字典序排列，每项写入UTF-8路径、NUL及该文件SHA256原始32字节，再计算总体SHA256。源码集合含Go源码/测试/示例、go.mod、嵌入HTML及复验脚本；testdata集合含所有固定文件和独立预期。它用于同一份输入复核，不是代码签名，也不证明SQL预期来源真实性。文档状态更新不改变解析器指纹。

## 覆盖与独立证据

| 维度/组合 | 固定资产与测试入口 | 证据边界 |
|---|---|---|
| 全部单表当前值 | 487快照；TestScanAllFixtures、rowio.TestAllFixtures | 484份完整/仅物化成功98811行、3份完整读取拒绝；SQL预期与原子/流式/两种导出往返 |
| 多类型键×ASC/DESC×多层树 | testdata/keys、TestKeyFixtures | 144新增快照24433行；[官方与SQL记录](../../testdata/keys/verification.json) |
| 无主键×隐藏ROW_ID×深树 | testdata/cluster、TestClusterFixtures | 12000行真实三层树；不是每类键都恰有相同树形 |
| UPDATE/DELETE×LOB/JSON×purge | testdata/changes、testdata/lob_updates，对应Fixtures/Damage测试 | 独立受控DML与当前值预期，不是MVCC恢复验证 |
| INSTANT×生成列×重建 | testdata/instant、testdata/generated，TestInstantFixtures/TestGeneratedFixtures | VIRTUAL显式物化、版本64、默认与物理位置，完整入口保留拒绝 |
| COMPACT/DYNAMIC×新旧LOB | testdata/compact，TestCompactFixtures | 44配对快照；旧链限同版本官方debug写入路径 |
| 查询×投影×前缀×延迟LOB | TestQuerySQL、TestSecondaryQuerySQL、TestSecondaryQueryNavigation | 二级查询358条SQL/27340结果行不是独立表行数；[证据](../../testdata/secondary_query/verification.json) |
| 分区×空表/LOB/COMPACT/DDL | 34文件、12集合，TestPartitionFixtures/TestPartitionOfficialSDI | 11成功集合3184行，1子分区拒绝；[官方/SQL记录](../../testdata/partitions/verification.json) |
| 空间×大文件×删除/重建 | 4空间专用快照，TestSpaceAssets/TestSpaceSnapshots | 另有487单表空间回归；跨XDES组、空闲残留，计数不混入读行基线；[证据](../../testdata/space/verification.json) |
| CLI失败与输出 | internal/cli的原子发布、子进程、取消、分区、visual测试 | SIGINT、断管、后段损坏、输入保护和预算；平台保证限实际运行环境 |
| 图与原始字节 | visual测试及[6类独立报告验证](../../scripts/verify_visual_reports.py) | 用户已确认关键浏览器交互；没有自动截图或跨浏览器验收 |

原始独立证据来自历阶段SQL采集、innochecksum和ibd2sdi记录，本次离线收尾复用这些固定预期，不声称重新执行官方工具或SQL服务。分区INNODB_INDEXES无本样本物理索引行，索引身份通过官方SDI验证，不称为SQL索引对照。

对布局拒绝和结构损坏的合成测试包括TestMetadataRejections、TestKeyMetadataRejection、TestCompactCorruption、TestChecksumDamage和TestPartitionMetadataDamage等。合成测试会重新封装CRC以进入深层校验，不能将其当作真实MySQL曾写出该损坏布局的证据。

## 故障复现与处理

以下命令使用临时副本。预期失败命令退出1，并显示错误类别或具体字段；不要用“能输出一部分”判断成功。

```sh
gzip -dc testdata/generated/mixed_instant.ibd.gz > /tmp/release-virtual.ibd
/tmp/innodb-reader check /tmp/release-virtual.ibd
# 上一命令完整列入口拒绝VIRTUAL；只需要物化视图时显式选择：
/tmp/innodb-reader check --materialized /tmp/release-virtual.ibd

# 范围/预算失败，不生成成功HTML：
/tmp/innodb-reader visualize --pages 999 testdata/mysql8045/lesson_rows.ibd
/tmp/innodb-reader visualize --max-read-calls 1 testdata/mysql8045/lesson_rows.ibd
```

| 现象 | 检查与下一步 |
|---|---|
| metadata成功但export失败 | 检查Issues、VIRTUAL及索引/列布局；只有确实接受物化视图时使用materialized |
| 单个分区不能自动读完整表 | 准备完整清单，核对缺失/重复/身份混配，见[80章](80-partition-validation-and-cli.md) |
| CRC/LSN、链或身份错误 | 核对快照稳定性、源文件和采集记录；保留出错字节，不关闭校验强行输出 |
| 超过16MiB完整值 | 接受类型上限；若只需原始值，可用可信引用的StreamLOB；查询未选该LOB可能仍成功 |
| ErrLimit | 根据报告确定哪个计量超限；只有资源允许时调整对应预算，不无条件取消限制 |
| 导出中断 | stdout前缀可能不完整；检查退出码、JSONL end或使用原子文件输出 |
| 无法覆盖目标 | 默认不覆盖属于预期；确认路径后使用overwrite，输入文件身份仍受保护 |
| 查询成功但check全表失败 | 查询只认证访问范围和实际返回值，未访问子树可能损坏或含未选超大值 |
| 空间或图成功但行读取失败 | 空间页结构不等于行字段、键顺序、LOB当前值已经认证 |

首版复验脚本另对lesson页4的一个副本字节翻转，验证CRC拒绝；对VIRTUAL失败导出验证原有目标字节保持不变。原文件从不修改。

## 性能与最终结果

基准输入为12000行`testdata/cluster/rowid_deep.ibd.gz`，读取已解压内存ReaderAt；基准计时不包含解压和磁盘I/O，Read保留完整结果，Scan使用空回调。3次迭代仅为本机观察，不构成性能SLA。内存实验强制GC并采样存活堆，排除输入及调用方额外保留，不是RSS、总分配量或持续运行的硬上限。

最终执行记录见[首版验收报告](../release-validation/report.json)，具体环境、缓存状态、fuzz次数、基准与存活堆以同目录日志为准。

本次结果（Go1.25.7，darwin/arm64，Apple M4）：完整race通过，核心/rowio/visual命中有效缓存，CLI重新执行9.003秒；覆盖率分别92.5%/89.7%/91.4%/89.2%。vet通过；Read/PartitionDirectory/Decoder/HTMLReport四项主动fuzz分别347914/7244/605370/18160次无失败。

3次基准Read约35.981ms、133457557 B/op，Scan约33.679ms、121940224 B/op；Read保留12000行的存活堆58963552字节，Scan1000/12000行采样峰值1154320/1211176字节。源码/夹具指纹一致，正常导出、VIRTUAL显式物化与拒绝、CRC/页号拒绝、失败覆盖保护全部通过。复验脚本自身的失败报告与现有目录保护另用隔离临时目录检查通过。
