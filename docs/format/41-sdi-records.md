# 41 SDI：从页 0 找到元数据 JSON

本章学习不提供用户列定义时，如何从独立表空间找到 SDI，读取 `(type,id)` 记录并还原原始元数据。前置内容是 [页与文件](02-file-to-page.md)、[记录链](03-page-to-record.md)、[索引树](06-index-tree.md) 和 [页校验](39-page-checksum.md)。

第十九阶段新增 `ReadSDI(io.ReaderAt, fileSize)`。它提取 JSON，不将 JSON 转换为用户表 schema；自动 schema 和用户索引根发现属于第二十阶段。因此能提取某文件的 SDI，不代表已有 Read 能还原该文件的全部用户数据。

## 源码基线

用户提供的本地源码根目录为 `/Users/kimihiro/workspace/codespace/cpp/mysql-8.0.45`，MYSQL_VERSION 确认为 8.0.45。后续优先从这里只读核查，不依赖下载或参考 Java 的版本推断。

相对于该源码根目录，本章对应：

- `storage/innobase/include/fsp0fsp.ic:384–393`：SDI 头的偏移公式。
- `storage/innobase/include/fsp0fsp.h:172,274–321`、`os0enc.h:117–142`：头部、区描述符与加密信息预留区大小。
- `storage/innobase/include/dict0dict.h:74`：SDI 物理版本为 1。
- `utilities/ibd2sdi.cc:83–138,1249–1262,1835–1960`：键/记录字段位置、入口读取与载荷提取。
- `storage/innobase/include/dict0sdi.h:40–88`：使用 zlib 压缩 SDI。

本轮也已直接核查 `storage/innobase/buf/checksum.cc:62–92,245–263`，确认上一阶段的 CRC 两区间异或和头尾比较实现与该版本一致。

## 页 0 保存入口，不固定猜页号

在当前 16 KiB 布局中，SDI 头偏移为：

```text
XDES_ARR_OFFSET                    = 150
XDES_SIZE                         = 40
描述符项数 = 16384 / 64           = 256
Encryption::INFO_MAX_SIZE         = 115
SDI头偏移 = 150 + 40×256 + 115    = 10505
```

其中 64 是当前页大小下一个 extent 的页数；115 字节即使文件未加密也在偏移计算中预留。不要删除预留区或把这个固定结果推广到其他页大小。

交付的 [lesson_rows.ibd](../../testdata/mysql8045/lesson_rows.ibd) 在文件 `[10505,10513)` 的真实字节为：

```text
00 00 00 01 | 00 00 00 03
物理版本 1      SDI 根页 3
```

读取过程先验证页 0 的 CRC、表空间布局和 SDI 标志，再检查物理版本与根页边界。从头中得到页 3 后，按 `3×16384=49152` 定位，要求页类型 SDI（17853）、space ID 一致，并沿该树读取。这里只是本样本根在 3；测试会把根搬到 9，验证代码确实使用入口字段。

## SDI 的固定记录结构

SDI 不需要用户 schema，因为其记录布局固定。叶子中的联合键是四字节无符号 type 和八字节无符号 id，按 type 优先、id 次优先排序；不能把十二字节塞进 uint64。两字段均为大端，不使用普通有符号 SQL 整数的符号位翻转。

```text
长度元数据 → 五字节记录头 → type(4) → id(8)
                              ↓
          trx_id(6) → roll_ptr(7) → 原始长度(4) → 压缩长度(4)
                              ↓
                  页内 zlib 字节 或 SDI_BLOB 引用
```

这些固定列非 NULL，没有用户列那样的 NULL 位图。非叶子只存 type、id 和四字节子页号，不存事务字段与 JSON 长度；MIN_REC、目录、链表和父子键范围仍需验证。

lesson_rows 共有两个对象：`(1,424)` 和 `(2,45)`，官方输出分别为 Table 和 Tablespace 对象。第二个键的 id=45 不是 FIL space ID=40，两者不能混为同一编号。本样本 SDI index ID 为 `18446744073709551615`，也不能按普通用户索引编号猜测。

## 逐字节读取 Table 对象

`(1,424)` 位于页 3，Start=441、origin=448、End=1499。origin 前的真实字节为：

```text
fa 83 | 00 00 18 fe bf
长度       记录头
```

从靠近记录头处反向读取，`83 fa` 的高标志表示两字节长度，得到 `0x03fa=1018`，未置 external 位。不是把内存中的 `fa83` 当作大端整数。

| 字段 | 页内范围 | 相对 origin | 真实值 |
|---|---|---:|---|
| type | [448,452) | 0 | `00000001` → 1 |
| id | [452,460) | 4 | `00000000000001a8` → 424 |
| trx_id | [460,466) | 12 | `000000006350` |
| roll_ptr | [466,473) | 18 | `82000000900226` |
| 原始 JSON 长度 | [473,477) | 25 | `000015ed` → 5613 |
| 压缩长度 | [477,481) | 29 | `000003fa` → 1018 |
| 压缩载荷 | [481,1499) | 33 | 开头 `78 9c ed 58 51 6f db 36` |

载荷文件绝对偏移为 `49152+481=49633`。本地记录数据长度是 `33+1018=1051`；加上七字节元信息，整个记录长度 1058。zlib 解压得到精确 5613 字节 JSON。

`(2,45)` 的 Start=120、origin=127、End=441，元信息为 `19 81 | 00 00 10 ff f1`，压缩长度281，原始长度478。两个记录的键顺序是 origin448 → origin127，说明链表顺序不等于物理位置顺序。

原始 Table JSON 中可以看到 `mysqld_version_id=80045`、`dd_version=80023`、`sdi_version=80019`。这些是 JSON 内容层的版本；入口头的物理版本1属于另一层。此阶段保持原文，不修改版本或解释列映射。

## API 与边界

[sdi.go](../../sdi.go) 返回 SDIResult，包含 SpaceID、RootPage、Version、IndexID、访问的 SDI 树 Pages 和按键排序的 Records。每个 SDIRecord 保留键、页号、记录范围、头部、事务字段、压缩/原始长度以及 `json.RawMessage` 原始解压字节；未重新编码 JSON，数字和文本不会经过 float64 中转。

`Pages` 不包含页0或 SDI_BLOB 页，后者由每条记录的 External.Chunks 标明。使用完函数后，调用方仍负责关闭文件；失败返回 nil，不输出部分对象。现有用户行 Read 的接口不变。

本阶段限可靠离线快照、16 KiB、非压缩非加密独立 DYNAMIC 表空间、普通 CRC32C、未删除的普通 SDI 记录。缺失 SDI/未知物理版本明确拒绝，不扫描猜入口，不自动修复。每对象压缩和解压各限16 MiB，累计解压限64 MiB，最多4096对象；这些是实现资源边界，不是 MySQL 格式上限。

下一章跟踪真实29页载荷，说明树验证、zlib边界和官方对照过程。
