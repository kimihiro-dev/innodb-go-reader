# 10 整数主键如何贯通整树扫描

学习目标：理解字段宽度为什么也影响非叶子页；理解不溢出的父子键范围；复跑真实 MySQL 对照，确认不是只有单个整数函数能返回正确数字。

## 1. 非叶子记录的长度随主键变化

沿用第 07 章，导航记录的结构是：

```text
[保留 NULL 位图][5 字节记录头] origin→[整数主键 w 字节][子页号 4 字节]
```

没有叶子记录的事务字段和普通列。子页号是独立的 4 字节无符号值，不能随主键宽度变化。本阶段测试 schema 有 11 个可空列，MySQL 8.0.45 为节点记录保留 2 个全零位图字节；依据仍是 `rem0rec.cc` 的 NODE_PTR 分支。

`mediumint_signed` 页 4、origin=169 的真实节点：

| 页内偏移 | 长度 | 实际字节 | 解码 |
|---:|---:|---|---|
| 162 | 2 | 00 00 | 保留位图 |
| 164 | 5 | 00 00 29 ff e4 | 普通节点头，非 MIN_REC |
| 169 | 3 | 7f ff d0 | MEDIUMINT −48 |
| 172 | 4 | 00 00 00 08 | 子页 8 |

整个记录 14 字节，数据部分 7 字节。固定按 INT 的 4 字节定位子页会读错。

页头的粗略数量校验也必须适应更短的记录。旧实现以 13 字节作为记录最小长度，新支持范围中最短节点可以是头 5 + TINYINT 1 + 子页 4 = 10 字节。`parseIndex` 使用这个保守下界；`decodeNode` 再按实际 schema 验证精确范围。真实 TINYINT 夹具曾触发旧下界误报，现在纳入回归测试。

## 2. 为什么不能统一转成 int64

无符号 BIGINT 最大值是 18446744073709551615，超过 int64。若转换后用普通有符号比较，它会变成 −1，使全局排序与父子范围判断错误。

实现保留公开列值的真实 Go 类型，比较时用 `integerOrder` 得到 uint64：

- 无符号值直接使用原值。
- 有符号值先扩展为 int64，再翻转第 63 位。这将有符号顺序映射到 uint64 的递增顺序。
- 仅比较同一个已验证主键类型；不定义不同 SQL 类型之间的比较语义。

例如有符号 BIGINT 的最小值、−1、0、最大值映射到 `0`、`2^63−1`、`2^63`、`2^64−1`。这是内存中的排序值，不是要求所有物理整数都读 8 字节。

页内顺序、跨页顺序、父子范围三处使用同一个映射。MIN_REC 的存储键仍不作为有限下界，保留前一阶段的哨兵语义。

## 3. 用“无上界”代替最大值加一

DFS 栈任务存储 low、high 及 bounded。low 包含边界，high 不包含边界；bounded=false 表示没有有限上界。

```text
根：low=0，bounded=false
某父页键：A、B、C
子区间：[继承下界,B)   [B,C)   [C,继承上界)
```

检查公式是：`key < low || (bounded && key >= high)`。

最右路径一直继承无上界标记，因此 uint64 最大值合法。无需计算 `18446744073709551615+1`，也无需大整数或特殊字符串。若父任务有上界，最后一个子节点则继承该有限上界。

## 4. 真实高位主键导航

`bigint_unsigned.ibd.gz` 的 root level=1，15 个索引页组成根与 14 个叶子。页 4、origin=298 的节点存储：

```text
页内 291: 00 00 | 00 00 59 ff 7b |
页内 298: 80 00 00 00 00 00 00 28 |
页内 306: 00 00 00 0e
```

无符号键为 9223372036854775848，子页为 14。下一节点键是 9223372036854775859，所以页 14 接受这个半开区间的行。两个端点都超过 int64 最大值，测试确保真正走过这种导航。

最右节点在页内 origin=355：键 `ff ff ff ff ff ff ff f9` 即 18446744073709551609，子页 17。其上界无限，第 09 章的最大值记录就位于该页。测试同时断言存在高位有限节点、末行等于 uint64 最大值。

## 5. 真实夹具与独立预期

本阶段固定资产位于 `testdata/integers`。manifest 保存版本、配置、数据库、root/space/index ID、文件长度及原始文件 SHA256。数据文件和 SQL 预期以 gzip 保存，哈希针对解压后的 .ibd。

| 主键类型 | 有符号行数 | 无符号行数 |
|---|---:|---:|
| TINYINT | 204 | 202 |
| SMALLINT | 204 | 202 |
| MEDIUMINT | 204 | 202 |
| INT | 204 | 202 |
| BIGINT | 401 | 501 |

合计 2526 行，每张表 root level=1。固定种子乱序插入；普通列覆盖最小、最大、零、NULL；主键另覆盖负数和边界。BIGINT 在极值附近密集插入，BIGINT UNSIGNED 还覆盖 2^63 两侧的密集值。

生成器通过 MySQL `SELECT JSON_ARRAY(...) ORDER BY id` 获取预期，Python 整数保留精度，Go 测试用 UseNumber 对照。预期结果不通过 Go 解析器生成。

最终快照数据库为 `innodb_reader_fixture_33e1abae3280`；初次较稀疏验证库 `innodb_reader_fixture_304006962e18` 也保留在本地。只新建专用数据库，没有修改已有用户表。

## 6. 验证内容

`TestIntegerFixtures` 校验哈希、行数、实际根层级、每列 SQL 值和 Go 返回类型；核对不同主键宽度下的事务字段偏移、节点大小。每种主键都进行重复键和错误父子边界的内存损坏测试，并验证节点子页号截断被拒绝。

`TestIntegerEncoding` 使用独立常量检验符号翻转、MEDIUMINT 符号扩展及 64 位极值。`TestIntegerSchemaAndTruncation` 检查非法 unsigned/max_chars/可空主键和截断列。`FuzzIntegerTree` 对真实无符号 BIGINT 文件进行有界随机改写，检查不会崩溃或在报错时返回部分表。

此前十组 14557 行仍全部回归；现在合计二十组 17083 行。Fuzz 通过不能证明任意损坏都能识别：尚未在 Go 读取路径核验 CRC，也不自动验证任意外部 schema。

## 7. 复现

在项目根目录执行：

```sh
go test ./...
go test -race ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzIntegerTree$' -fuzztime=10s -parallel=2
gzip -dc testdata/integers/bigint_unsigned.ibd.gz > /tmp/bigint_unsigned.ibd
go run ./examples/read /tmp/bigint_unsigned.ibd testdata/integers/bigint_unsigned.json
```

最后一条输出 501 行 JSON。新建快照可用以下命令，先在终端安全设置 MYSQL_PWD，不要把密码写进脚本或文档：

```sh
python3 scripts/generate_fixtures.py --integers \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-integer-fixtures
```

输出目录必须不存在。生成器使用唯一库名，同一连接持有 FOR EXPORT 锁，获取预期并复制全部文件后解锁。重新生成会改变事务字节和哈希；本手册的偏移和字节对应已交付固定资产。

对解压文件使用 MySQL 的 `innochecksum` 进行官方校验。本阶段十个文件已全部通过。完整验证记录见项目 TODO；这一阶段仍保留物化全部结果的 API，内存随数据量增长。
