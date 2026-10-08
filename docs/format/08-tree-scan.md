# 08 完整扫描索引树并验证结果

学习目标：看清整树遍历过程，追踪一条真实行，理解不丢行、不重复、有序的验证证据。前置知识：第 06–07 章。

## 1. 显式栈里的任务

每个遍历任务保存页号、期望层级、允许的主键区间 `[low,high)`。主键为 int32，但区间用 int64，初始上界可表示为 2147483648，因此最大合法 INT 不会被误排除。

步骤：

1. 读取并检查 FSP 第 0 页，从输入根页建立第一个任务。
2. 弹出任务，拒绝访问过的页，读取该页并核对层级、ID 和同层链关系。
3. 沿页内活动记录链解码：叶子得到用户行，非叶子得到导航记录。
4. 叶子行验证父区间和全局主键顺序后加入结果。
5. 非叶子为每条子指针建立范围，按逆序压栈，使出栈时按键从小到大访问。
6. 遍历结束时，每层最后一页的 Next 都必须为无后继。

这里没有递归调用栈，恶意层级或循环不会造成无界递归；重复页由 visited 集合拒绝。算法依赖索引指针，不会把文件中所有 INDEX 页都当作当前表。

## 2. 两层树的完整访问过程

顺序样本根页 4 解码得到：

```text
MIN_REC → 页5，范围 [-2147483648,-277)
键 -277 → 页6，范围 [-277,178)
键 178  → 页7，范围 [178,2147483648)
```

先压入页 7，再页 6，最后页 5。弹出顺序为 5、6、7。访问页 5 解码 223 行，再访问页 6 解码 455 行，最后页 7 解码 322 行，总共 1000 行。根页的三条导航记录保存在 `Result.Nodes`，不进入 `Result.Records`。

`Result.Pages` 顺序是 `[4,5,6,7]`。乱序样本相应为 `[4,5,8,6,7]`；保证的是键顺序，不是数值页号排序。

## 3. 跟踪 id=4203 到实际文件字节

深树中路径为：

```text
页4 level2：键4203 → 页38
页38 level1：键4203 → 页633
页633 level0：第一条用户记录 origin133，id=4203
```

最后主键字节的绝对文件位置：

```text
633 × 16384 + 133 = 10371205
该位置四字节 = 80 00 10 6b
翻转符号位后 = 00 00 10 6b = 4203
```

在 origin 前 13 字节为 `fc fc fc fc fc fc fc fc 00 00 10 07 fe`：八个 252 字节长度 + 五字节普通记录头。后续仍按第 04 章的规则读取 6B 事务 ID、7B 回滚指针与八个字符串。本阶段没有重写叶子类型解码。

可以亲自验证：

```sh
python3 - <<'PY'
import gzip
from pathlib import Path
b=gzip.decompress(Path('testdata/trees/deep_rows.ibd.gz').read_bytes())
for page,origin in [(4,138),(38,125),(633,133)]:
    off=page*16384+origin
    print(page,off,b[off:off+8].hex(' '))
PY
```

前两个位置是键加子页号；第三个位置已是叶子主键加事务字段开头，不能继续把主键后的四字节当作子页号。先看层级/记录状态再解码，是这一阶段最关键的分支。

## 4. 三组互相补充的完整性检查

**页内结构**：活动/free 链不循环、不相交，heap number 唯一，记录不重叠，目录 owner 与活动记录数匹配。

**树结构**：每个子页只访问一次；space/index ID 必须相同；子页层级比父页低一；用户键和有限导航键必须落在父区间；MIN_REC 的位置正确。

**跨页顺序**：同层 DFS 顺序与 FIL prev/next 互相一致；首末页边界正确；全部输出主键严格递增。

这些检查覆盖明确的结构矛盾，不是密码学完整性验证。若文件被协调修改成另一棵结构自洽的树，仍需要可信快照、页校验和及外部数据基准辨别。当前 Go 仍不计算页 CRC。

## 5. 独立 SQL 对照和回归

新样本共 14500 行，全部列逐一与导出锁保护期间的 SQL JSON 结果比较。原有七组共 57 行继续通过。深树样本真实 level=2，不使用合成页伪装三层。

```sh
go test ./...
go test -run 'TestTreeFixtures|TestCorruptTree' -v .
go test -race ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzTree$' -fuzztime=10s -parallel=2
```

`TestTreeFixtures` 另从第一叶子页沿 Next 独立行走，比较完整叶子集合及顺序与 DFS 完全一致。损坏测试覆盖子页号 0/越界、循环、重复指针、错误层级/ID、MIN_REC 错误、保留位图错误、父键范围、叶子链以及 free 链重入活动记录。

三个新原始夹具均通过 MySQL 8.0.45 `innochecksum`。SHA256 指向解压后的字节，验证测试资产未被更改。

2026-09-09 实测：test/race/vet 全部通过，核心包语句覆盖率 92.8%；10 秒 FuzzTree 执行 98046 次，无 panic/死循环。模糊测试针对真实两层样本的字节变异，不能代替所有格式的证明。示例程序另实际输出 1000 行，检查其与 SQL 预期一致。

## 6. 使用现有示例读取跨页表

gzip 只是交付包装，先解压：

```sh
python3 - <<'PY'
import gzip
from pathlib import Path
Path('/tmp/ordered_rows.ibd').write_bytes(gzip.decompress(Path('testdata/trees/ordered_rows.ibd.gz').read_bytes()))
PY
go run ./examples/read /tmp/ordered_rows.ibd testdata/trees/ordered_rows.json > /tmp/ordered_rows.jsonl
wc -l /tmp/ordered_rows.jsonl
```

应有 1000 行。原示例 API 无需变更。`Read` 仍返回完整结果：Page 保留根页；Pages 包含访问页；Nodes 包含非叶子导航信息；每条 Record 新增 PageNumber，Start/Offset/End 仍是页内位置。

内存随着行数、页数增长，目前不是流式导出。错误返回 nil，不交付看似完整的部分行；本阶段没有引入并行缓存或数据库连接。

## 7. 重新生成

沿用第 05 章的安全密码输入示例，调用生成器时增加 `--tree`，并选择不存在的新目录。脚本建立唯一命名测试库，保存顺序与固定种子乱序的 SQL；同一会话持有 FOR EXPORT 锁直到 SQL 基准读取及文件复制结束。

本阶段专用库为 `innodb_reader_fixture_7e30cb376715`，已保留供复查，不修改前两次夹具库或其他用户表。源码生成器会拒绝实际根层级不符合预期的结果。

新能力仅扩展页数和树层数，以及合法页分裂残留。类型仍为有符号 INT 与 utf8mb4 VARCHAR(1..63)/NULL；仍需可信 schema、显式根页，仍不支持 INSTANT、LOB、其他键类型或事务历史恢复。
