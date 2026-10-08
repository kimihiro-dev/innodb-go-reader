# 39 页校验：两个 CRC32C 区间和头尾 LSN

本章学习：读取到一页后，如何验证其校验字段、区分字节损坏和结构错误，并理解校验的覆盖边界。前置内容是 [文件与页](02-file-to-page.md)、[整树读取](08-tree-scan.md) 和 [LOB 路径](13-lob-reference-and-pages.md)。本阶段维持 16 KiB、非压缩非加密表空间，读取路径要求当前 crc32 校验格式。

## 校验位置与计算

InnoDB 的 crc32 格式使用 CRC32C（Castagnoli），不是常见 IEEE CRC32。Go 使用 `hash/crc32.MakeTable(crc32.Castagnoli)`。页面保存的整数以大端解释，这与 CRC 的位运算方向是不同问题。

```text
页内偏移  0     4                    26          38                  16376   16380 16384
          ├ CRC ┤   校验区间 A       ├ 不参与CRC ┤    校验区间 B       ├ CRC尾 ┤ LSN尾 ┤
                       ↓                                  ↓
                    CRC32C(A)          XOR            CRC32C(B)
                                         ↓
                           必须同时等于 CRC头 和 CRC尾
头部 LSN [16,24) 的低32位 [20,24) ─────────────── 必须等于 LSN尾
```

| 页内范围 | 长度 | 含义/检查 |
|---|---:|---|
| [0,4) | 4 | 头部校验值，不参与自身计算 |
| [4,26) | 22 | 页号、前后页、LSN、页类型，区间 A |
| [26,34) | 8 | 历史 flush LSN 等字段，不参与本格式 CRC |
| [34,38) | 4 | space ID，不参与本格式 CRC，由归属检查验证 |
| [38,16376) | 16338 | 页体与页目录，区间 B |
| [16376,16380) | 4 | 尾部校验值 |
| [16380,16384) | 4 | LSN 低32位副本 |

精确公式为：

```text
C = CRC32C(page[4:26]) XOR CRC32C(page[38:16376])
head_crc == C && tail_crc == C
header_lsn_low32 == trailer_lsn_low32
```

两次 CRC 分别初始化和结束。不能对整页计算，也不能把 A、B 拼起来只计算一次。两个校验字段相等也不够：它们可能同时保留着修改前的旧值，必须与重新计算的 C 比较。

官方 [WL#5652](https://dev.mysql.com/worklog/task/?id=5652) 说明 CRC 结果保存在两个校验字段中；[MySQL 8.0.45 buf0checksum.h](https://dev.mysql.com/doc/dev/mysql-server/8.0.45/buf0checksum_8h.html) 列出当前 CRC、旧算法与 legacy-big-endian 变体。当前库只实现普通 CRC32C，不声称复制 MySQL 所有兼容回退策略。本地 Java `InnerPage.java:75–88` 提供两个区间计算参考，`Ut0Crc32.java` 提供 Castagnoli 实现线索；本阶段以真实 8.0.45 文件、独立逐位 CRC 实现和同版本官方工具交叉验证。

## 真实页逐步核对

使用一直沿用的 [lesson_rows.ibd](../../testdata/mysql8045/lesson_rows.ibd)，不生成新表。聚簇根页为页 4，文件基址 `4×16384=65536`。

```text
[0,4)       bc f5 b4 04
[4,26)      00 00 00 04 ff ff ff ff ff ff ff ff
            00 00 00 00 1c a9 6f e4 45 bf
[26,38)     00 00 00 00 00 00 00 00 00 00 00 28
[16376,end) bc f5 b4 04 1c a9 6f e4
```

逐步计算得到：

```text
CRC32C(A) = 0x16ae0078
CRC32C(B) = 0xaa5bb47c
异或结果   = 0xbcf5b404
头部 CRC   = 0xbcf5b404
尾部 CRC   = 0xbcf5b404
头尾低 LSN = 0x1ca96fe4
```

尾部 CRC 的文件绝对偏移是 `65536+16376=81912`，尾部 LSN 从 81916 开始。页号、LSN、space ID 和 CRC 是这份快照的实测值，不是固定格式常量。

同一文件前五页分别为 FSP_HDR、IBUF_BITMAP、INODE、SDI 和 INDEX；计算结果依次为 `418a386e`、`5215f894`、`67b8063f`、`5b18dbeb`、`bcf5b404`。测试用这些原始常量与独立逐位 CRC 同时断言，避免实现和测试共用错误公式却一起通过。

## 接入读取路径

[checksum.go](../../checksum.go) 提供内部 pageCRC/verifyPageChecksum；[page.go](../../page.go) 的 readPage 在完整读取后、返回页面前执行检查。所有使用 readPage 的表空间头、索引页与 LOB 页都受保护，公开 Read 参数和返回类型不变。错误示例：

```text
page 4 at file offset 65536: invalid InnoDB data: CRC32C mismatch: ...
```

顺序是完整读取 → 全零判断 → 头尾 LSN → CRC → 页号 → 后续页类型/归属/记录结构检查。CRC 错误属于 ErrCorrupt，公开 Read 返回 nil 结果；即使已成功解码前面几页，也不会返回看似完整的部分表。

页面没有可靠的“校验算法标签”。仅凭失配无法断言是损坏、旧算法还是禁用校验，因此错误说明 `strict crc32 format required`，不猜测算法，不自动接受 none、innodb 或 legacy-big-endian。该要求是当前输入契约，不表示所有被拒绝文件都无法被 MySQL 正常读取。

## 全零页、未访问页与一致性

lesson_rows 的页 5、6 全零，但 Read 只访问页 0 和根页 4，可以正常还原全部行。直接引用全零页则报告“未初始化或损坏”，不能返回为有效记录页。全零检查不是空间分配位图解析，也不能仅凭某页校验失败推断其已分配/未分配状态。

本阶段校验**实际读取路径**，不扫描所有文件页。损坏未访问的 SDI 页不会阻止当前显式 schema 读取；完整文件检查仍可使用官方 innochecksum。后续页枚举/分配分析属于其他阶段。

CRC 不覆盖 [26,38)，不能检测所有位翻转；space ID 由原有归属验证保护。CRC 有碰撞可能，头尾 LSN 相等也不能证明事务已提交或文件来自一致备份。输入仍需可靠离线快照，不能直接把活动实例文件的读取结果解释成事务一致 SELECT。
