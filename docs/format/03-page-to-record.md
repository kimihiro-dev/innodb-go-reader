# 03. 从数据页到记录

学习目标：识别记录 origin、读取五字节头、沿逻辑记录链遍历，并理解页目录。前置知识：[第 02 章](02-file-to-page.md) 中的页内偏移和 INDEX 头。

<a id="1-origin-为什么不等于整条记录起点"></a>

## origin 为什么不等于整条记录起点

在当前格式中，一条用户记录有如下组织：

```text
低地址                                              高地址
变长长度区 | NULL 位图 | 固定记录头 5 B | 主键 | 系统字段 | 其余用户列
                                      ↑ origin
↑ Record.Start                        ↑ Record.Offset                ↑ End
```

记录指针指向主键起点。固定记录头就在它前面 5 字节，NULL 位图与变长长度又在记录头前面。NULL 列和变长列使每条记录的总长度可能不同，因此不能假设“从第一行起每隔固定字节就是下一行”。

`Record.Start` 包含该记录实际存在的全部前置元信息，`End` 是列数据结尾的后一字节。它们用于解释布局并检测两条记录是否重叠。

<a id="2-两条系统记录"></a>

## 两条系统记录

新式页面里：

| 名称 | 固定头位置 | origin | 数据字节 |
|---|---|---:|---|
| infimum | `[94,99)` | 99 | `[99,107)` 为 `infimum\0` |
| supremum | `[107,112)` | 112 | `[112,120)` 为 `supremum` |

它们分别充当逻辑下界和上界，不是用户数据。遍历从 infimum 的 next-record 开始，到 supremum 结束。空表仍有这两个结构，此时 infimum 直接指向 supremum。

本样本 infimum 固定头为 `01 00 02 00 38`；supremum 固定头为 `05 00 0b 00 00`。程序同时核对固定状态、heap number、名称、终止指针，不只搜索字符串。

<a id="3-五字节记录头如何拆解"></a>

## 五字节记录头如何拆解

以 `id=-3` 的记录为例，origin=155，固定头 `[150,155)`：

```text
00 00 18 00 43
│  └─┬─┘ └─┬─┘
│ heap/status next-record
info/n_owned
```

| 相对 origin | 长度 | 解释 | 本记录 |
|---|---:|---|---|
| −5 | 1 | 高 4 位 info flags，低 4 位 n_owned | 均为 0 |
| −4 | 2 | 高 13 位 heap number，低 3 位 record status | `0x0018 >> 3 = 3`；status=0 |
| −2 | 2 | 指向下一记录 origin 的相对偏移编码 | `0x0043 = 67` |

status：0 为普通记录，1 为非叶子子页指针记录，2 为 infimum，3 为 supremum。本步只解码普通叶子用户记录，并专门处理两个系统记录。

info 中含删除标志、INSTANT/行版本等布局信息。本步拒绝非零用户记录 info flags，避免把另一种布局按本阶段 schema 解码。

heap number 表示堆内记录身份，不是主键，也不是每次按主键遍历的序号。例中插入第二条记录 `-3` 的 heap number 是 3；插入第一条 `7` 的 heap number 是 2。

<a id="4-相对偏移能向前也能向后"></a>

## 相对偏移能向前，也能向后

MySQL 8.0.45 的 `rec_get_next_offs` 对 compact 格式使用相对编码，并限制在同一页内。本项目页大小为 16384，计算为：

```text
delta = 两字节大端无符号数
delta == 0 → 无后继
否则 next = (origin + delta) & (16384 - 1)
```

主案例完整链如下。所有 origin 都是页内偏移：

```text
infimum(99)
   │ +56
   ▼
id=-3 (155)
   │ +67
   ▼
id= 2 (222)
   │ -95
   ▼
id= 7 (127)
   │ +62
   ▼
id=12 (189)
   │ -77
   ▼
supremum(112) → 无后继
```

`222 → 127` 的编码为 `ff a1`。按无符号数解释是 65441，`(222+65441)&16383 = 127`；按有符号 16 位看则是 −95。两种解释在当前页大小下得到相同位置。

不能把 `ff a1` 直接当作页内绝对地址，也不能假设记录指针总向更大的文件偏移移动。实现见 `record.go/nextRecord`；这里使用页内环绕规则，随后仍检查目标是否位于合法记录区。

<a id="5-物理顺序与逻辑顺序"></a>

## 物理顺序与逻辑顺序

主案例实际记录区：

| 物理字节区间 | origin | id | heap number |
|---|---:|---:|---:|
| `[120,148)` | 127 | 7 | 2 |
| `[148,182)` | 155 | −3 | 3 |
| `[182,216)` | 189 | 12 | 4 |
| `[216,239)` | 222 | 2 | 5 |

这里物理顺序恰好对应插入顺序；逻辑链则是 −3、2、7、12。以后涉及删除、空间复用、页重组，不能把当前物理排列当成始终成立的插入历史。

<a id="6-页目录不是逐行偏移数组"></a>

## 页目录不是逐行偏移数组

页目录存储若干记录 origin，各槽由拥有一组记录的 owner 表示。目录从页尾逆向存储，每槽两字节：

```text
slot i 的存放偏移 = 16384 - 8 - 2×(i+1)
```

主案例两个槽：偏移 16374 的 `00 63` 指向 infimum；偏移 16372 的 `00 70` 指向 supremum。infimum 的 n_owned=1；supremum 的 n_owned=5，拥有 4 条用户记录加自身。

为了验证不只有两个目录槽的情况，`directory_rows` 包含 40 行、9 个槽。按逻辑顺序读取槽值为：

```text
[99, 1245, 1084, 919, 754, 589, 424, 259, 112]
```

槽指向的记录位置也不要求数值递增。代码在遍历记录链时累计 owner 管辖记录数，并核对 n_owned 与目录位置；第一步尚未用目录二分来加速主键点查。

<a id="7-怎样知道没丢行也没陷入循环"></a>

## 怎样知道没丢行，也没陷入循环

`record.go/Read` 检查：

- origin 必须在用户记录堆内，不能进入页头、目录或页尾。
- 同一 origin 不能访问两次，heap number 必须唯一且合法。
- 每条解码记录占用区间不能与另一条记录重叠。
- 主键必须严格递增，目录 owner 关系必须一致。
- 最终到达 supremum，解码条数与 INDEX header 的用户记录数相同。

如果出现损坏，返回 `ErrCorrupt`；已知不支持的状态返回 `ErrUnsupported`。不会以“已读到部分行”伪装为成功。

<a id="8-自己追踪一次"></a>

## 自己追踪一次

```sh
python3 - <<'PY'
from pathlib import Path
b = Path('testdata/mysql8045/lesson_rows.ibd').read_bytes()[65536:81920]
origin = 99
for _ in range(8):  # 独立演示仍设置步数上限
    header = b[origin-5:origin]
    delta = int.from_bytes(header[3:], 'big')
    nxt = (origin + delta) & 16383 if delta else 0
    print(origin, header.hex(' '), '->', nxt)
    if origin == 112:
        break
    origin = nxt
PY
```

你应读到 `99 → 155 → 222 → 127 → 189 → 112 → 0`。下一章从其中一条记录展开到所有列值。

格式依据：[MySQL 8.0.45 rem0rec.ic](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/rem0rec.ic)，特别是 `rec_get_next_offs`、heap number 与 info bits 读写函数。
