# 69. 聚簇键点查与范围导航

本章目标：从查询键下降到目标叶页，解释为什么不必先读取整表，以及一次成功查询究竟验证了哪些内容。前置为[多类型键布局](53-typed-key-layout.md)、[键排序验收](54-key-order-validation.md)和[流式协议](67-streaming-contract.md)。本阶段沿用已经支持的聚簇键、字符集和格式，不扩展二级索引查询或事务可见性。

<a id="一查询契约先于导航"></a>

## 查询契约先于导航

`Key` 是按聚簇索引成员顺序排列的 `[]any`，单列也必须放在数组中。它可以描述显式主键、实际选中的唯一非空聚簇键，或一个六字节隐藏 ROW_ID。隐藏 ROW_ID 是物理身份，不是用户 SQL 列。

```go
q := innodb.KeyRange{
    Lower: &innodb.KeyBound{Key: innodb.Key{int64(5988)}, Inclusive: true},
    Upper: &innodb.KeyBound{Key: innodb.Key{int64(6012)}, Inclusive: true},
    Reverse: true,
    Limit: 10,
}
report, err := innodb.QueryAuto(ctx, file, size, q, innodb.ScanOptions{},
    func(event innodb.ScanEvent) error {
        if event.Record != nil {
            // 在这里消费完整当前物理行；回调同步执行。
        }
        return nil
    })
// 必须同时检查 err == nil 和 report.Complete。
```

边界按实际索引顺序比较，每个成员都保留 ASC/DESC。对于单列 DESC 索引，闭区间 Lower=9、Upper=3 选中从9到3的键；Reverse=true 返回3到9，但不改变所选集合。不能因反转输出再交换边界。

| 请求 | 含义 |
|---|---|
| `PointKey(Key{6000})` | 相同完整键的闭区间；不存在时成功零行 |
| `Lower/Upper == nil` | 对应一侧无界；两侧均 nil 时选全部键 |
| 完整键 Lower 大于 Upper | 空区间，不自动交换 |
| 相等边界且任一侧开区间 | 空区间 |
| `Prefix: Key{tenant, code}` | 完整前两列相等，可用于更长复合键 |
| `Prefix: Key{"ab"}` | 第一列等于“ab”，不是以“ab”开头 |
| `Prefix: Key{}` | 无效；nil Prefix 才表示未指定 |
| `Limit: 0` | 不设查询行数上限 |

Prefix 与 Lower/Upper 互斥；边界必须是完整键，Prefix 必须包含1到全部键成员。比如 `(tenant ASC, code DESC, seq ASC)` 的范围 `[ (1,"b",0), (1,"a",100) ]` 沿物理顺序从 b 走向 a，内部仍以 seq 升序比较。字符相等遵循对应排序规则的等价类，包括 PAD SPACE，不是简单 Go 字符串相等。

四个入口是 `Query`、`QueryAuto`、`QueryMaterialized`、`QueryMaterializedAuto`。前两个保留严格完整行约束，含 VIRTUAL 时拒绝；后两个显式选择物化列并在报告中单独给出 VirtualColumns。带 Auto 的入口先读 SDI，其他入口使用调用方可信 schema。

<a id="二非叶子记录代表半开子范围"></a>

## 非叶子记录代表半开子范围

一个非叶子记录包含键与子页号。普通键是该子树的有限下界，下一个导航键是上界，所以子范围为 `[lo, hi)`。最左 MIN_REC 哨兵继承父范围的下界，不能把它保存的显示键误当有限边界。末项继承父范围的上界，无父上界时为正无穷。

查询与某个子范围不相交，就不把该子页加入 DFS 栈。闭下界等于子树的排他上界时，该子树仍不可能命中。前缀比较需要更谨慎：如果上界与请求的部分前缀相等，子树内仍可能包含更小的后续成员，必须保留这个边界子树。

```text
请求完整键6000
       根页4（level2）
       导航键4203 → 子页38
              ↓
       页38（level1）
       导航键5995 → 子页889
              ↓
       页889（level0）→ 命中6000

其他不相交子树不入栈；叶页中未命中行不跟随LOB。
```

`query.go:entryRange` 先在已经验证的目录 owner 分组上二分，再在选中分组内部二分。`tree.go:walkSelectedTree` 据此选择导航项或叶子记录；Read/Scan 通过同一核心的无选择器路径保留全树行为。Reverse 改变入栈和页内交付方向，不收集全部结果后再倒序。

目录查找减少候选比较，子树剪枝减少页读取。为了保持本地结构检查，访问页仍完整解析记录链、堆、目录和页内键序，因而不能宣称页内全部 CPU 工作都是对数复杂度。

<a id="三用真实字节复核下降路径"></a>

## 用真实字节复核下降路径

样本为已有、未修改的 `testdata/trees/deep_rows.ibd.gz`，12000行，16KiB页，整树共1718个聚簇页。解压后整数按大端存储，有符号 INT 的最高位翻转。

| 位置 | 文件绝对偏移 | 原始字节 | 含义 |
|---|---:|---|---|
| 根页4，记录 origin=138 | 65674 | `80 00 10 6b 00 00 00 26` | `0x106b=4203`，child=`0x26=38` |
| 页38，origin=3453 | 626045 | `80 00 17 6b 00 00 03 79` | `0x176b=5995`，child=`0x379=889` |
| 叶页889，origin=10363 | 14575739 | `80 00 17 70` | `0x1770=6000` |

这些偏移是记录载荷起点，不是五字节记录头起点。前两行包含四字节键和四字节子页号，最后一行只展示键；事务字段与行载荷继续按原记录布局解释。根和该叶页的目录均为 `[99,112]`；少量目录槽不代表只有两个用户记录，supremum 可以拥有记录组。

可独立复核：

```python
import gzip
from pathlib import Path
b = gzip.decompress(Path("testdata/trees/deep_rows.ibd.gz").read_bytes())
for page, origin, length in [(4,138,8), (38,3453,8), (889,10363,4)]:
    offset = page * 16384 + origin
    print(offset, b[offset:offset+length].hex())
```

显式 schema 的点查只访问3个聚簇页，PageReads=4（另含表空间头）。同一请求 QueryAuto 的实测报告为 Pages=3、Nodes=2、Records=1、PageReads=8、CacheHits=3、PhysicalReads=5、TraversalEntries=1126；SDI发现和根核对也消耗请求，因此不能混淆聚簇页数与物理 I/O。闭区间5988..6012共25行，正反向都访问6个聚簇页。

<a id="四局部校验与完成状态"></a>

## 局部校验与完成状态

每个访问页仍执行 CRC/LSN、索引身份、层级、完整本地记录/目录/顺序及父范围校验；相邻访问页检查双向 sibling 链，跨叶检查完整键序。非叶子页的零子指针即使落在未选中的项也会被拒绝。未访问子页的结构与数据不作认证，查询边缘不要求整层链首/链尾为 NULL。

因此未访问叶页损坏可以不影响一次点查，而访问路径损坏必须失败。无界 Query 仍采用查询的局部契约，不替代要求全树链尾完整性的 Scan。显式 schema 下可证明为空的范围不读文件；Auto 仍需先读取元数据才能解释键。空查询的 Complete 不意味着文件健康。

Page/Node 事件是路径证据，其键可能在范围之外。Record/DeletedRecord 按范围筛选；delete-mark 只交付摘要，不占 Limit，不跟随删除 LOB。命中行完整还原页外值，范围外 LOB 不解引用，但同页记录的本地结构仍被检查。

`QueryReport` 嵌入 ScanReport，并增加 LimitReached：

| 结果 | Complete | LimitReached | 含义 |
|---|---|---|---|
| 自然完成、零命中或空范围 | true | false | 请求范围完成 |
| 成功交付 Limit 条当前行 | true | true | 请求上限已满足，不证明还有更多行 |
| 回调错误/ErrStopped/ErrLimit/取消/I/O或结构错误 | false | 通常false | 已输出前缀不可撤回，按错误处理 |

LimitReached 独立记录已成功交付的行数上限；之后边界取消等错误仍可使 Complete=false。报告计数包含返回错误的那次回调，Limit 只在回调成功后计入；资源 MaxRows 与查询 Limit 不同，前者超限报错。页请求、遍历项、缓存和单行源字节预算继续遵守[第68章](68-streaming-lob-and-budgets.md)。

下一章以独立 SQL、损坏注入及精确键编码验证这些契约。
