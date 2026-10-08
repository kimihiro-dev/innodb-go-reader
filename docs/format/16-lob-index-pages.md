# 16. 多页 LOB 索引：归属链与数据顺序

<a id="学习目标"></a>

## 学习目标与前置概念

第 13 章介绍了首个 LOB 页的十个索引项。本章继续回答：超过十块时索引项放在哪里？为什么不能直接按 FIL_NEXT 拼接数据？怎样从真实文件证明没有少读一块？前置概念是 20 字节引用、6 字节文件地址（页号 4 + 页内偏移 2，大端）、60 字节索引项。

## 外部索引页布局

LOB_INDEX 页类型为 22，它保存索引项，不直接保存用户数据。16 KiB 页布局如下，数字都是页内偏移：

```text
0             38 39                                16359 16376 16384
| FIL header  |v| 272 × 60 字节索引项                 |余量 |尾部 |
```

| 字段 | 偏移 | 长度 | 解释 |
|---|---:|---:|---|
| FIL_PAGE_OFFSET | 4 | 4 | 当前页号，大端 |
| FIL_PAGE_NEXT | 12 | 4 | 下一张已分配索引页，大端；ffffffff 结束 |
| FIL_PAGE_TYPE | 24 | 2 | 22，即 0016 |
| space ID | 34 | 4 | 所属表空间，大端 |
| 格式版本 | 38 | 1 | 当前仅接受 0 |
| 索引项 | 39 | 16320 | 槽位起点为 39 + 60×k，0≤k<272 |

容量是 `floor((16384−39−8)/60)=272`。最后合法起点为 16299，16359 已不属于槽位。LOB_FIRST 的槽位仍在 96..696，起点是 96+60×k，共十个。两种页面不能采用相同的对齐基准。

每项沿用第 13 章的 60 字节结构：相对偏移 0 是 prev 地址，6 是 next 地址，12 是历史版本链，48 是数据页号（4 字节），52 是本块长度（2 字节），56 是 LOB 版本（4 字节）。注意块长度不是四字节字段。数据仍来自首个页偏移 696 或 LOB_DATA 页偏移 49。

布局和分配行为核对了官方 [lob0impl.h](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/lob0impl.h#L492)、[lob0impl.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/lob/lob0impl.cc#L70) 及 [lob0first.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/lob/lob0first.cc)。

## 两条链承担不同职责

首个 LOB 页的 FIL_NEXT 指向最近分配的索引页，新页再指向旧页。这条链用于找出“哪些索引页属于这个 LOB”，顺序通常与分配时间相反。

活动索引链则从 LOB_FIRST 的页内 68 处取得首项地址，逐项读取 next。它决定用户值的数据块顺序，可能先走旧索引页再走新索引页。不能按页号排序，也不能按 FIL_NEXT 顺序拼接。

`readExternal` 分两步处理：先加载和验证分配链，建立页号到索引页的局部映射；再沿活动链读取每个索引项，只允许访问归属映射内合法槽位。验证 prev、重复槽位、数据页重复、版本、块长度及最终端点，最后要求块长度之和等于引用长度。初始只插入布局需要的外部索引页数量为 `ceil(max(块数−10,0)/272)`；此约束不能直接套用到未来的更新/历史版本支持。

## 真实跨页边界

`testdata/large_lob/medium_index.ibd.gz` 的解压 SHA256 为 `088ed2a12976590434ba40b97589e98055c73c1faeae481e176d4e2192a0b354`。四行 MEDIUMBLOB 的值重复 `00 ff 80` 并截到指定字节数：

| id | 字节数 | 数据块数 | LOB_FIRST 页号 | 外部索引页数 |
|---:|---:|---:|---:|---:|
| 0 | 162623 | 10 | 5 | 0 |
| 1 | 162624 | 11 | 15 | 1 |
| 2 | 4603567 | 282 | 27 | 1 |
| 3 | 4603568 | 283 | 337 | 2 |

首块容量为 15680，普通块容量为 16327。`15680+9×16327=162623`；再加一个字节便需要第十一项，不能再放入首个页。另一边界是 `15680+281×16327=4603567`，已经用满十个首项和 272 个外部项。

id=1 的第十项位于页 15 偏移 636，其 next 原始字节为 `0000001a0027`，表示页 26、偏移 39。页 26 的首项 prev 是 `0000000f027c`（15:636），next 是 `ffffffff0000`，数据页号 `00000019`（25），块长度 `0001`，版本 `00000001`。它仅贡献最后一个字节，不能因块太小而跳过。

id=3 的两条链尤其直观：

```text
分配链：LOB_FIRST 页 337 → 索引页 621 → 索引页 348 → FIL_NULL
活动链：337:96 … 337:636 → 348:39 … 348:16299 → 621:39 → NULL
```

页 621:39 的 prev 原始字节为 `0000015c3fab`，解码得到 348:16299；数据页号为 `0000026c`（620），块长度 `0001`。其文件绝对位置为 `621×16384+39=10174503`。沿分配链直接拼接会把最后一字节放到前面。

`Record.External[].Chunks` 按活动链排序。新增 `IndexPage` 与 `IndexOffset` 共同定位索引项；`PageNumber/Offset/Length` 定位数据块。Record.End 仍只描述聚簇页本地记录；Result.Pages 仍只列出聚簇索引树页面。

## 夹具、验证与复现

六组新快照通过同一 MySQL 会话执行 FOR EXPORT、SELECT、文件复制后 UNLOCK，来自专用库 `innodb_reader_fixture_0e5bc55c533d`，未修改既有用户表。SQL、schema、独立预期和原文件哈希均随 `testdata/large_lob` 交付。

| 夹具 | 行数 | 验证目的 |
|---|---:|---|
| all_lob_types | 7 | 八种类型、0/1/127/128/255、NULL、emoji、任意二进制 |
| text_blob | 3 | NULL、空值和两列各 60000 字节页外值 |
| medium_index | 4 | 10/11/282/283 项，跨两张外部索引页 |
| large_text | 1 | 300000 字节 MEDIUMTEXT、2000000 字节 LONGTEXT，完整 UTF-8 |
| long_limit | 1 | 16777216 字节 LONGBLOB 完整还原 |
| long_over_limit | 1 | 超出实现上限一字节，ErrUnsupported 且结果为 nil |

本阶段五组成功样本 16 行，另一组是限制拒绝样本。全项目累计 35 组成功样本、17560 行逐列 SQL 对照；总计 36 组物理资产，不能将拒绝样本计作成功还原。测试还逐块核对公开来源信息与文件实际字节。

损坏测试覆盖归属链缺失/循环/越界、错误页号/类型/空间/格式、头部或尾部非法槽位、活动链循环与前驱错误、历史版本、重复数据页及计数和长度异常。所有错误都不返回部分表。六个新文件通过官方 innochecksum；test/race/vet 通过，核心包覆盖率 96.8%；FuzzLOBIndex 运行 10 秒，7728 次执行，无失败。

离线复现：

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzLOBIndex$' -fuzztime=10s -parallel=2
gzip -dc testdata/large_lob/all_lob_types.ibd.gz > /tmp/all_lob_types.ibd
go run ./examples/read /tmp/all_lob_types.ibd testdata/large_lob/all_lob_types.json
```

重新生成使用 `scripts/generate_fixtures.py --large-lob`，连接参数按脚本 `--help` 传入，密码通过环境提供。生成器将客户端 max_allowed_packet 调到 128M，以接收 HEX 编码后翻倍的大值，不修改服务器配置。生成会新增唯一专用库，应使用测试实例；已有离线资产足以运行回归。

当前 API 将整表结果保存在内存中，16 MiB 是单值限制，不是整表内存预算。仍不支持历史 LOB、压缩/旧格式、自动元数据发现或事务可见性；后续阶段应独立规划，不能从本章推断已能读取任意表。
