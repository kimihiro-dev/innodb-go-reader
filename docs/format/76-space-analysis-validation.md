# 76 空间分析接口、指标与验收

[第75章](75-space-allocation-layout.md)解释分配字节，本章说明如何使用报告，以及“严格”具体认证哪些事实。

## 原子接口

```go
report, err := innodb.AnalyzeSpace(ctx, file, size, innodb.SpaceOptions{})
if err != nil {
    // report == nil；不能把中间统计当成功结果。
    return err
}
fmt.Println(report.FilePages, report.UsedPages, report.FreePages)
```

不要求Schema，也不物化LOB或用户列。错误立即返回nil报告；合法空闲/未初始化页正常分类，未知使用中页在验证CRC/LSN/FIL身份和分配归属后保存Raw原页，不猜其内部格式。没有独立Complete字段：成功返回本次分配分析报告，失败没有部分报告。

SpaceOptions.MaxPages默认1000000，限制输入物理页数；MaxEntries默认10000000，累计限制页面、分配链、INODE槽、段页、记录链和索引同层链等遍历。它们不表示行数或LOB字节数。不复用ScanOptions，以免MaxRows语义混淆。取消保留context错误，I/O错误保留原错误链，损坏结构ErrCorrupt，超预算ErrLimit，格式不在输入范围内ErrUnsupported。

报告保留每页摘要、区和段列表；未知使用中页额外保留整页Raw，因此内存随页数和未知页字节量增长，不承诺流式常量内存。返回切片独立于输入缓冲，调用方可以保留。页内容先按分配结构分类，再决定是否验证使用中页；描述页提前读取并复用，真实样本PhysicalReads恰等于FilePages。

JSON示例：

```sh
gzip -dc testdata/space/empty.space.gz > /tmp/space-empty.ibd
go run ./examples/space /tmp/space-empty.ibd
```

示例一次输出成功报告，出错只向stderr报告并退出非零，不输出部分JSON。64位IndexID/SegmentID按整数编码，下游应使用能保留64位精度的JSON解析器。

## 报告中的指标

| 字段 | 分母/含义 |
|---|---|
| FilePages、SizePages、FreeLimit | 文件物理长度、FSP大小、初始化边界，三者分开 |
| UsedPages、FreePages、UninitializedPages、TailPages | 互斥文件页分类，总和等于FilePages |
| ZeroPages | 全零内容观察量，与分配分类重叠 |
| UsedPageTypes | 仅使用中页类型计数，不含空闲页残留类型 |
| Extent.Used | 位图中64个FREE位为0的个数，包括描述区公共页 |
| Extent.Present | 该区在FSP_SIZE以内实际存在的页数 |
| Extent.SegmentID | 原XDES_ID，仅state4/5表示当前整区所有者 |
| Segment.ReservedPages | 当前段保留的整区实际页＋碎片页；租用区扣公共2页 |
| Segment.UsedPages | 上述保留范围内当前占用页；不包含其他段或公共元数据 |
| Index.Pages、LeafPages、Height | 当前分配的索引页数量、叶页数量、根Level+1 |
| Index.PhysicalRecords | 叶页活动记录链项总数，含delete-mark，不含PAGE_FREE链 |
| Index.LOBPages | 归属于索引段的已知LOB/SDI_BLOB类型页数，不代表当前字段值页数 |

INDEX页局部空间用独立字段表达：

- `HeapBytes = HeapTop - 120`：普通记录堆区域，不含系统记录。
- `GarbageBytes = PAGE_GARBAGE`：头部记载的可回收字节，可含复用后留下的小碎片。
- `LayoutUsedBytes = HeapBytes - GarbageBytes`：根据头部核算的活动布局字节，不声称逐字段解码得到。
- `DirectoryBytes = Slots * 2`：当前目录实际字节。
- `ContiguousFreeBytes = 16384 - 8 - DirectoryBytes - HeapTop`：堆顶到目录之间连续空间。
- `Records`、`DeletedRecords`、`FreeRecords`：当前物理链项数、其中delete-mark项数、PAGE_FREE链项数，均不是SQL事务快照行数。

相应页内恒等式是 `120 + HeapBytes + ContiguousFreeBytes + DirectoryBytes + 8 = 16384`。GarbageBytes已经包含在HeapBytes里，不能再向整页大小相加。若需要布局使用率，可明确使用LayoutUsedBytes/HeapBytes；HeapBytes为0时该比率应标为未定义，不能除零。若需要物理占用页比例，可使用UsedPages/FilePages。两者不是同一个指标，代码不提供混合分母的单一“填充率”。

## 严格验证的范围

分配验证包含：FSP大小与初始化边界、描述状态与FREE位、空间三类extent链、INODE页链和槽、段三类extent链与碎片数组、唯一所有权、页身份及公共页保留、计数互证、根FSEG定位、索引页段身份和同层唯一双向链。

INDEX局部验证复用现有头部边界检查，并验证紧凑系统记录、记录状态、heap编号、活动/空闲链、记录头不重叠、目录owned和MIN标记。该入口不依赖列schema，因此不验证字段长度/字段值、索引键比较、父子导航键或具体LOB活动/历史链。需要这些保证时，使用既有Read/Query/ReadSecondary及LOB接口；不能把空间分析成功当成全部SQL值、所有B+树导航或MVCC正确的证明。

已证实空闲页可能有非零旧记录甚至失效CRC；报告中的HeaderNumber、HeaderSpaceID、Type等是观察值，ChecksumVerified=false。当前归属仅由分配证据决定。类型未知不等于损坏，也不等于内部结构已认证。报告不抢救行、不恢复事务历史，不扩展通用/系统/压缩/加密表空间或其他页大小。

## 新快照与独立对照

新库 `innodb_reader_space_2b493d901bb5` 位于用户提供的8.0.45测试实例。只修改该新库，使用FOR EXPORT采集；实例保持运行。普通主键、二级索引及每行9000字节LONGBLOB共同制造跨extent和跨XDES组分配。资产见[testdata/space](../../testdata/space/README.md)。

| 快照 | SQL行 | 文件页 | 使用中 | 空闲 | 未初始化 | 全零 |
|---|---:|---:|---:|---:|---:|---:|
| empty | 0 | 8 | 6 | 2 | 0 | 2 |
| grown | 17000 | 17408 | 17080 | 136 | 192 | 328 |
| deleted | 10 | 17408 | 18 | 17198 | 192 | 328 |
| rebuilt | 10 | 18 | 16 | 2 | 0 | 1 |

grown有269个初始化描述、6个segment、3棵索引（含SDI）；主键57页、56叶页，普通二级17页、16叶页，SDI1页；主键段关联17000个LOB页。页16384/16385实际验证第二组XDES/IBUF_BITMAP，包含一个状态5的满租用区，详细字节见75章。

deleted的文件没有缩小，FREE_LIMIT仍17216，释放出来的页保存旧内容。官方页类型摘要仍列74个普通INDEX和17000个Other（新式LOB），与grown相同；当前分配分析则只计2个普通INDEX、1个SDI和10个LOB。不能用页头扫描数量代替活动空间。rebuilt换为space820、主键/二级ID1045/1046，根页号仍4/5，证明页号本身不是稳定索引身份。

4文件共34842页通过官方innochecksum严格crc32。8个SDI对象与ibd2sdi和Go SDI示例一致，SQL表空间/索引ID/根页与报告对应。官方page-type-dump对已识别类型逐页比较编号、类型及INDEX的ID/Level/Records/Garbage；22/23/24新式LOB在官方工具中归Other且不产生dump行，单独核对其汇总数和总页数。另用Python按原始XDES位独立重算所有文件页的分配类别。

`.sql-metadata.json.gz`是SQL独立预期；`.official-pages.txt.gz`、`.official-summary.txt`和`.sdi.json.gz`保存官方证据；`.analysis.json.gz`是Go报告留档，不能反过来充当独立正确性预期。manifest保留原始文件SHA及采集顺序，verification.json保存验证结果。

## 回归与错误测试

全部487份既有原始资产通过空间分析，包括原完整读行拒绝的3份、旧COMPACT BLOB、新LOB、INSTANT、隐藏ROW_ID、生成列、二级索引与变更/purge/复用快照。旧资产不修改，仍保留484份完整/仅物化成功98811行、3份完整读取拒绝的原基线。新增4份`.space.gz`专用于空间验收，不加入逐行/逐点查询矩阵；空间分析共验证491份快照。

错误测试覆盖FSP大小/边界/计数、描述状态与保留位、断链/环、INODE magic/重复ID、根inode引用/层级、页号、位图位置、使用中全零与CRC、系统space ID0/索引ID0、合法空闲残留、未知使用中页原始字节所有权，以及I/O/取消/页数与遍历预算。INDEX同层链和记录链检查避免空转和重复计数。FuzzSpaceAllocation以真实小表为种子、单字节变异后重算CRC，最终代码十秒执行495751次无失败，验证错误无部分报告、页数恒等式及碎片归属。

复跑命令：

```sh
GOCACHE=/private/tmp/innodb-go-reader-stage30-cache go test -run 'TestSpace|FuzzSpace' .
GOCACHE=/private/tmp/innodb-go-reader-stage30-cache go test -race -cover -timeout=25m ./...
GOCACHE=/private/tmp/innodb-go-reader-stage30-cache go vet ./...
python3 scripts/verify_space_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
```

全量race/coverage通过（857.205秒），核心包覆盖率92.6%；身份边界补充后的全部空间专项race（15.278秒）及vet通过。详细结果在阶段TODO记录；测试超时沿用第35阶段的25分钟，产品预算没有放宽。[生成器](../../scripts/generate_space_fixtures.py)要求新输出目录，自动创建独立新库并从终端读取密码；[验证器](../../scripts/verify_space_fixtures.py)仅使用离线快照和本地官方工具。
