# 68. LOB 分块、资源预算与内存验收

本章目标：不物化完整LOB也能读取原始字节，并区分预算、累计分配、存活内存和进程RSS。先读[流式协议](67-streaming-contract.md)及[新旧页外格式](65-compact-row-layout.md)。

<a id="一逐行流式不等于字段分块"></a>

## 逐行流式不等于字段分块

Scan逐行交付完整Record：文本仍是string，二进制仍是[]byte，JSON仍是JSONValue。每个字段必须先完整解码，因而继续受16 MiB物化上限约束。

`StreamLOB(ctx, reader, size, source, options, yield)`是独立原始块流。source为ExternalField，但只使用Reference和Prefix；它重新解析原始引用，不信任派生Version、Length、Chunks等字段。已有小值可用Read返回的ExternalField；超大值不必先成功Read，也可以用可信记录中的20字节引用和0或768字节前缀构造输入。

调用方负责引用和前缀属于同一稳定、可信记录。独立块流会检查页/空间/链/当前LOB版本，却不能仅凭引用证明记录归属、列类型或MVCC可见性。它不把裸引用扫描称为完整行恢复。

每个LOBBlock包含：

| 字段 | 含义 |
|---|---|
| Offset | 从完整值开始的逻辑字节偏移，包含本地前缀 |
| Prefix | true表示本地COMPACT前缀，其Chunk没有页外来源 |
| Chunk | 页外块来源；新LOB含索引项地址，旧BLOB没有索引项 |
| Data | 调用方独立持有的原始字节 |

前缀首先输出，随后按逻辑值顺序输出页外块。单块最多一个页的载荷，可能切断UTF-8字符或二进制JSON对象。StreamLOB不验证完整文本/JSON/GEOMETRY类型；需要类型值时使用现有物化路径。

<a id="二同一条校验路径"></a>

## 同一条校验路径

`lob.go:walkExternal`根据实际首页类型分派新LOB和旧BLOB；物化readExternal只是在外层累积块并保存Chunks。COMPACT仍把768字节本地前缀接在后缀之前。

新LOB继续验证分配索引链、活动链、历史项、版本、DATA块身份/长度以及精确链尾。区别在于只长期保存索引页号集合，索引页内容按需读取，交给有界缓存处理；不为大字段保留全部索引页的16 KiB载荷。历史项不作为当前块输出。旧链仍严格走BLOB header next，算法见[第66章](66-compact-lob-validation.md)。

每个块只表示已通过目前涉及的检查，后续页错误仍可能导致整条流失败。只有LOBReport.Complete与nil错误共同确认整链结束。Bytes/Blocks统计已调用回调的字节/块数，包括返回错误的那一块。

<a id="三超过16-mib的真实原始流"></a>

## 超过16 MiB的真实原始流

复用已有 `testdata/large_lob/long_over_limit.ibd.gz`，不修改其“类型物化超限”的拒绝属性。根页4的首条记录origin=128，INT主键4字节、事务字段6字节、roll pointer7字节后，是页内145、绝对65681处的20字节引用：

```text
00 00 00 5b | 00 00 00 05 | 00 00 00 01 | 00 00 00 00 | 01 00 00 01
space=91      first=5       version=1      flags/high     length=16777217
```

这比16 MiB多1字节。原子Read和完整值Scan依旧返回ErrUnsupported；StreamLOB输出1028块，累计16777217字节，其SHA256和长度与保存的SQL HEX结果相同。

```sh
gzip -dc testdata/large_lob/long_over_limit.ibd.gz > /tmp/long-over.ibd
go run ./examples/streamlob /tmp/long-over.ibd \
  0000005b00000005000000010000000001000001 > /tmp/value.bin
```

原始字节写stdout，完成报告写stderr；非零退出时value.bin可能只有前缀。该例子是固定可信夹具的引用，不能复制去读取另一个表。

COMPACT前缀的跨字符证据继续使用第65章：前缀末字节为e7，后缀首两个字节为95 8c，合起来才是“界”。分块接口不补字符，也不替调用方选择字符编码。

<a id="四预算的计量对象"></a>

## 预算的计量对象

ScanOptions的零值使用下列默认值；提高预算必须显式赋值。缓存为-1表示禁用，其他小于-1的值拒绝。

| 配置 | 默认 | 计量对象 |
|---|---:|---|
| CachePages | 64 | 最多缓存64个完整页，载荷约1 MiB |
| MaxPageReads | 1000000 | 页请求，包含缓存命中与自动入口的SDI读取 |
| MaxEntries | 1000000 | 累计排队树任务、物理记录项及LOB页/索引项遍历 |
| MaxRows | 1000000 | 当前未删除行，不把删除摘要当用户行 |
| MaxRowBytes | 64 MiB | 单行本地记录字节＋页外后缀＋补入的INSTANT默认字节 |
| MaxLOBBytes | 64 MiB | 仅独立LOB块流的完整值原始字节，含前缀 |

所有计数到达上限不自动算成功：需要继续消费下一项才报ErrLimit；若刚好完成，仍允许最终检查。LOB总长已在引用中给出，超过MaxLOBBytes会在输出第一块之前拒绝。

单行预算在LOB物化前检查，但本地页解码及INSTANT默认的已有处理可能先发生。它不是精确Go堆计量；string、TextBytes、JSON节点、切片与map都有额外占用。自动入口的SDI解压另沿用单对象16 MiB、累计64 MiB、4096对象限制。不能将CachePages或MaxRowBytes写成“进程最多使用这些字节”。

LRU以页偏移为键，只缓存成功的完整页读取。命中仍复制到本次读取缓冲并执行原有页校验；回调不能修改缓存。淘汰复用旧缓存页缓冲，避免遍历每个新页都再分配一个缓存载荷。MaxPageReads在命中前计数，故缓存不会绕过遍历预算。报告PhysicalReads计实际底层调用次数（包括失败调用），CacheHits计命中；两者之和为接受的PageReads。

<a id="五内存结果怎样读"></a>

## 内存结果怎样读

在darwin/arm64、Apple M4上，使用12000行的真实rowid_deep表，输入文件已在内存中；测量基线扣除输入与测试框架，回调不保留事件。通过主动GC在第1行、每128行及终点采样存活堆：

| 模式 | 行数 | 基线之上的存活堆 |
|---|---:|---:|
| Read，保留完整结果 | 12000 | 约59 MB |
| Scan，1000行提前停止 | 1000 | 约1.2 MB采样峰值 |
| Scan，完整扫描 | 12000 | 约1.2 MB采样峰值 |

这是**GC后采样存活堆**，不是连续峰值监控，也不是RSS硬上限。它证明此夹具无需同时保留整表；循环检测等状态仍随访问页数增长。调用方若保留Record，内存会相应增长。

`BenchmarkTableScan`另测耗时与B/op/allocs/op。B/op是整次操作的累计分配，不能据它说瞬时内存等量；解码和返回独立值仍需要分配。主动GC测量会扰动时间，所以两类实验分开跑，不承诺固定倍数提速。本次各3轮的实测：Read约37.2 ms/op、133459381 B/op；Scan约34.8 ms/op、121940218 B/op。累计分配仍明显大于存活堆，不能把这两个数混用。

```sh
INNODB_SCAN_MEMORY=1 go test -run '^TestScanMemory$' -v
go test -run '^$' -bench '^BenchmarkTableScan$' -benchtime=3x -benchmem
```

<a id="六验证与边界"></a>

## 验证与边界

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go test -run '^$' -fuzz '^FuzzStreaming$' -fuzztime=10s
go test -run '^$' -fuzz '^FuzzStreamLOB$' -fuzztime=10s
python3 scripts/verify_streaming.py
```

流式重组与现有Read在全部469份资产上对照：467份成功共87476行，2份保持类型/布局拒绝；359个页外值分别重新分块、拼接并还原为同样的值和Chunk来源。原有SQL、官方SDI及原始文件SHA回归继续执行，未新增或改写原始快照。CLI验证9份代表性快照12620行，检查完整/错误报告、行预算和新旧LOB及超限原始流。

错误测试覆盖预先取消、回调取消、底层读取返回时取消、首行停止、自定义回调错误、途中I/O失败、CRC损坏、全部行已输出后的链尾错误、预算恰好够用/超出、缓存关闭/淘汰/所有权、VIRTUAL严格拒绝和原子Read无部分结果。FuzzStreaming结合页内容变异和停止位置检查计数/完成协议。

源码：[scan.go](../../scan.go)、[stream_lob.go](../../stream_lob.go)、[lob.go](../../lob.go)、[compact.go](../../compact.go)；实验：[scan_bench_test.go](../../scan_bench_test.go)；示例：[scan](../../examples/scan/main.go)、[streamlob](../../examples/streamlob/main.go)。主键点查和范围扫描属于下一阶段，本阶段提前停止不等于实现了索引范围查询。
