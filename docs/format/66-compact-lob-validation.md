# 66. 旧 BLOB 页链与 COMPACT 验证

本章接[COMPACT 行布局](65-compact-row-layout.md)。目标是手工走完一条旧链，说明哪些证据支持恢复出的完整值，并复跑配对验收。

<a id="一旧链页头"></a>

## 旧链页头

在 debug 路径真实样本 `testdata/compact-legacy/lesson_compact_initial.ibd.gz` 中，首条 txt 的首页为 5。绝对位置 `5×16384=81920`，页内字节如下：

| 页内位置 | 字节 | 含义 |
|---|---|---|
| 24..25 | `00 0a` | FIL_PAGE_TYPE_BLOB=10 |
| 34..37 | `00 00 00 02` | space ID=2 |
| 38..41 | `00 00 3f ca` | 本页载荷 16330 字节 |
| 42..45 | `00 00 00 06` | 下一 BLOB 页为 6 |
| 46.. | `95 8c f0 9f 98 80 e7 95 ...` | 接在本地前缀末尾的原始后缀 |

页尾保留 8 字节，所以单页载荷上限为 `16384−46−8=16330`。尾页的 BLOB next 必须为 `ff ff ff ff`。链路使用偏移 42 的 BLOB next，不使用 FIL 页头偏移 12 的通用 next。测试独立改写 FIL_NEXT 并重算 CRC，证明解析没有把两种链混用。

源码依据：本地官方 `storage/innobase/include/lob0lob.h:141–148` 定义长度/next 共 8 字节；`storage/innobase/lob/lob0ins.cc:178–251` 写首引用、头和载荷；`storage/innobase/lob/lob0lob.cc:750–778` 沿 BLOB header 读取。文件版本统一为 8.0.45。

普通样本的页 5 则为 type=24，第一块位于 696，长度 15680；下一块在页 6 偏移 49，长度 16327。它们来自新 LOB 索引项，不允许用旧链的 46/16330 常量解释。

<a id="二读取算法与失败条件"></a>

## 读取算法与失败条件

1. 检查引用和整值预算：非零后缀、空间身份、有效首页、已知状态、前缀加后缀不超过列容量及 16 MiB。
2. 检查首个页的 CRC/LSN、页号、space 和 type，分派新/旧格式。旧引用头偏移必须为 38。
3. 旧链每页验证 part length 为正，未越过页尾，也不超过尚缺的后缀长度。
4. 记录访问集合，拒绝重复页、0 页号、越界、额外页和提前结束；每页均做原有 CRC/LSN 校验。
5. 总载荷恰好等于引用 Length 且链结束后，拼接本地前缀，再进行完整类型解码。

公开读取入口在任何失败时不返回部分结果。结构损坏测试先重算 CRC，以确保错误抵达链检查；另有不重封装的位翻转专门验证校验和拒绝。测试覆盖循环、0/越界 next、提前结束、零/过大块长、错误 space/type、头偏移、being-modified 标志、错误后缀长度、非法文本前缀及 RowFormat 不匹配。前缀所有权、列容量和包含前缀的资源上限另有直接测试。

<a id="三样本矩阵"></a>

## 样本矩阵

普通 writer 与官方 debug `lob_insert_noindex` writer 各 22 份快照、3866 行。每组内部的 COMPACT/DYNAMIC 执行相同逻辑 SQL，保存独立 hand schema、SQL 结果、完整 ibd、官方 SDI、DDL、索引身份和 writer SQL。

| 组 | 每种行格式的快照 | 验证重点 |
|---|---:|---|
| lesson | 5 | LONGTEXT/LONGBLOB/VARCHAR/VARBINARY/JSON，增长、格式混合、缩短、NULL、删除 |
| bounds | 1 | 0/1/767/768/769、约 8 KiB、旧链单/双/三页边界及 NULL |
| tree | 3 | 600→601 行、102 页树、STORED/VIRTUAL、INSTANT 非末尾 ADD、重建 |
| hidden | 1 | DB_ROW_ID、重复用户行、页外值、NULL |
| composite | 1 | 767 字节 DESC 键成员加 INT，100 行非叶子树 |

旧链 COMPACT 的完整值 17098、17099、33428、33429 字节分别对应 1、2、2、3 个页外块，因为 768 字节保留在记录中。该数字是“已经外置后的链容量边界”，不是 InnoDB 判断外置的统一阈值。

debug 组的 mixed_formats 快照先关闭会话调试点再更新 txt，同一行因此同时含新 LOB txt 和旧 BLOB bin。此样本验证按字段实际页类型分派，而不是每表只选一次格式。普通组所有阶段保持普通 writer；不能将表名或阶段名当物理格式证据。

SQL 按完整聚簇键排序；隐藏键表按包含重复计数的多重集合比较。二进制值对照 HEX；JSON 使用普通视图和精确数字语义比较。测试还核对每个 Reference、Prefix、Chunk 的源文件切片，重新拼接后必须等于类型值。

<a id="四离线复跑"></a>

## 离线复跑

在仓库根目录运行：

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go test -run '^$' -fuzz '^FuzzCompactLOB$' -fuzztime=10s
python3 scripts/verify_compact_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
python3 scripts/verify_compact_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin --fixtures testdata/compact-legacy
```

验证脚本检查 SHA256、官方 innochecksum 严格 CRC32、ibd2sdi 与保存的原始对象、SDI CLI、严格/仅物化入口及 SQL 输出。`verification.json` 和 `physical.json` 是派生报告，SQL/官方工具输出才是独立基准。

重新采集使用 `scripts/generate_compact_fixtures.py --mysql /path/to/mysql --socket /path/to/mysql.sock --out /new/directory`；debug 实例另加 `--legacy`，程序验证版本为 8.0.45-debug。输出目录必须新建。生成器只创建独立新库并设置会话变量，使用 FOR EXPORT 复制；生产实例不应直接照搬 debug 配置。

<a id="五适用范围"></a>

## 适用范围

本阶段覆盖 MySQL 8.0.45 的 16 KiB 非压缩非加密独立表空间，两种行格式、上述当前值与 DDL 组合。旧 BLOB 样本是官方同版本 debug 写入路径产生的实际文件，不能作为 5.7 或任意旧版本兼容的证据。REDUNDANT、压缩 BLOB、MVCC 历史恢复和 VIRTUAL 求值继续不在范围内。资源限制仍按单值和现有整树 API 执行；流式读取属于下一阶段。
