# 67. 逐行扫描、部分输出与完成协议

本章目标：在不保留整表的情况下消费当前物理行，并正确判断一次读取是否完整。前置概念为[整树扫描](08-tree-scan.md)、[COMPACT 与页外分派](65-compact-row-layout.md)，生成列和当前版本限制仍按[手册目录](README.md)中此前章节执行。本阶段不扩展文件格式、SQL类型或事务可见性。

<a id="一为什么只在-read-外加循环没有用"></a>

## 为什么只在 Read 外加循环没有用

`Read` 的结果包含 Pages、Nodes、Records 和 DeletedRecords，适用于需要完整结果的调用方。错误时它返回 nil，不交出半张表。若先执行 Read，再逐行调用回调，整表已驻留内存，无法解决这一阶段的问题。

现在两种入口共享 `tree.go:walkTree`：

```text
稳定 ReaderAt + schema
        ↓
同一套页校验 → 记录/目录校验 → 父子范围/全局顺序 → LOB/类型还原
        ↓
      一个事件
        ├─ Read：收集进Result，全部完成才返回
        └─ Scan：立即同步回调，交付后允许释放
                             ↓
                       最后检查各层链尾
                             ↓
                       Complete=true
```

没有后台 goroutine，没有无界队列。回调尚未返回时，不继续读取下一项。这提供了自然背压：写文件、计算哈希或发送给下游的速度决定消费速度。

<a id="二四个入口与四种事件"></a>

## 四个入口与四种事件

| 入口 | schema来源 | VIRTUAL处理 |
|---|---|---|
| Scan | 调用方可信schema | 拒绝 |
| ScanAuto | SDI自动发现 | 拒绝 |
| ScanMaterialized | 调用方可信schema | 显式只读取物化列 |
| ScanMaterializedAuto | SDI自动发现 | 显式只读取物化列 |

四个入口都接收 context、ReaderAt、文件大小、ScanOptions 和同步 `func(ScanEvent) error`，返回 `(ScanReport, error)`。显式schema入口在options前多一个Schema参数。

每个事件恰好包含以下一项：

- Page：DFS访问的聚簇页，出现在该页的条目事件之前。
- Node：非叶子导航信息，仍不是用户行。
- Record：完整的当前物理行，Values和LOB值类型与Read一致。
- DeletedRecord：delete-mark摘要，不伪装当前SQL行，也不跟随删除LOB。

事件里的切片和数据可保留，也可由回调修改；扫描器不复用交付的载荷。调用方不能在扫描期间修改传入schema、源文件或快照。保留所有事件会重新产生接近Read的内存需求；流式优势来自消费后释放。

ScanReport中的Columns/VirtualColumns解释输出列。仅物化入口的Values列号均为物化Columns下标，不给VIRTUAL填nil。报告在函数返回时获得；若需要在第一个回调前按列名处理，可先InspectTable，再把所选schema交给显式入口。那次单独的InspectTable不计入后续Scan预算。

<a id="三完整不等于已经看到了最后一行"></a>

## 完整不等于已经看到了最后一行

```go
report, err := innodb.ScanAuto(ctx, file, size, innodb.ScanOptions{},
    func(event innodb.ScanEvent) error {
        if event.Record != nil {
            return consume(*event.Record)
        }
        return nil
    })
if err != nil || !report.Complete {
    // 先前consume的调用不可撤回；由业务决定丢弃、回滚或保留为前缀。
}
```

只有 `err == nil && report.Complete` 才表示全部约定校验完成。页事件说明该页已通过本地解码，不证明后代或整张表完整。末尾还有各层Next链尾检查，因此可能已经输出所有行，最终仍因多出的Next指针失败。

报告的Records、Pages、Nodes、DeletedRecords统计**已经调用回调的次数**，包括回调返回错误的那次；它们不是业务提交计数。回调可能做了一半操作才返回错误，库不能推断外部副作用是否成功。

| 结束原因 | error | Complete | 已输出前缀 |
|---|---|---|---|
| 全部校验完成 | nil | true | 完整 |
| 调用方提前停止 | ErrStopped，可包装 | false | 保留 |
| 取消或超时 | context错误，可包装 | false | 保留 |
| 预算耗尽 | ErrLimit，可包装 | false | 保留 |
| I/O或结构/类型失败 | 原因可由errors.Is判断 | false | 保留 |

提前停止示例：回调在第100行返回 `innodb.ErrStopped`。这意味着“调用方选择停止”，不意味着已验证文件剩余部分。ScanOptions.MaxRows则是资源预算，不是SQL LIMIT；遇到第N+1行时返回ErrLimit。如果表恰好只有N行，仍可继续完成链尾校验并成功。

<a id="四取消与生命周期"></a>

## 取消与生命周期

context在读取前后、遍历项和回调边界检查。已取消的context不会开始I/O。`io.ReaderAt`没有context参数，所以不能强制打断底层无限阻塞的ReadAt；回调阻塞也需要调用方自己配合context。库不会启动一个无法清理的后台goroutine来制造“即时取消”。

文件仍由调用方打开和关闭，扫描期间必须保持稳定。取消不会关闭调用方文件；成功也不会关闭。原子Read接口没有新增默认预算或取消行为，原有应用兼容。

<a id="五真实树与物理来源保持不变"></a>

## 真实树与物理来源保持不变

本阶段复用 `testdata/cluster/rowid_deep.ibd.gz`，解压后37748736字节。根页4的绝对起点为 `4×16384=65536`：

| 位置 | 字节 | 解码 |
|---|---|---|
| 页内64..65，绝对65600..65601 | `00 02` | 根level=2，三层树 |
| 页内54..55，绝对65590..65591 | `00 03` | 根包含3个导航记录 |

这不是通过物理页号顺序输出行；仍按根、子指针及完整键范围进行DFS。真实样本有12000行，流式逐事件重组后的Page/Pages/Nodes/Records/DeletedRecords与原子Result一致，包括ROW_ID、引用、事务字段和删除原始字节。

`walkTree`只保留当前页的解码条目、待访问任务、各层链尾和重复页检测集合。已交付叶子条目会从当前页工作列表清除，避免同一叶子上的多个大LOB继续被扫描器持有。循环检测不能随意删除，因此它仍有与已访问页数相关的状态；由遍历预算约束，不承诺严格O(1)内存。

<a id="六可运行示例"></a>

## 可运行示例

```sh
go run ./examples/scan testdata/mysql8045/lesson_rows.ibd
# 对含VIRTUAL的文件明确选择物化视图：
go run ./examples/scan -materialized /path/to/table.ibd
# 资源耗尽会非零退出，stdout可能已经有前缀：
go run ./examples/scan -max-rows 1 testdata/mysql8045/lesson_rows.ibd
```

示例输出JSON行：每个事件使用 `event` 包装，最后使用 `report`，失败时另有 `error`。消费者必须检查最终报告与进程退出码，不能把stdout存在当作成功。如果stdout自身写失败，最终报告也可能写不出，此时退出码尤其重要。

实现见[scan.go](../../scan.go)、[tree.go](../../tree.go)、[CLI](../../examples/scan/main.go)。下一章解释预算、LOB块流与内存测量。
