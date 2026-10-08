# 30 BIT：全位宽、NULL、LOB 与整树验证

本章建立 [BIT 字节解码](29-bit-encoding.md) 的验证闭环：用真实 SQL 数值确认全部位宽，再核查记录长度、NULL 和页外数据没有因新增类型发生错位。

## 真实样本矩阵

所有资产位于 [testdata/bits](../../testdata/bits/manifest.json)，MySQL 8.0.45、16 KiB、DYNAMIC、独立非加密非压缩表空间。gzip 仅压缩交付文件，不是 InnoDB 压缩格式。

| 表 | 行数 | 覆盖 | 根 level |
|---|---:|---|---:|
| bit_widths | 69 | 64 个 BIT 列，位宽 1..64；全 NULL、零、最大值、AA/55 交替位、逐个置位 | 1 |
| bit_mixed | 4 | 主键重排、两字节 NULL 位图、不同位宽、VARCHAR、60000 字节页外 TEXT | 0 |
| bit_tree | 600 | 打乱插入、BIT(64) 高值、BIT(9)/NULL、长文本、跨页扫描 | 1 |
| 合计 | 673 | 全行逐列与 SQL 独立预期比较 | |

`bit_widths` 的 id=0 为全 NULL，id=1 为全零，id=2 为每列的最大值；id=3/4 分别使用 AA/55 交替位并按位宽截取；id=5..68 逐个设置第 0..63 位，超过该列宽度的位截成零。测试因此能检测字节序、位移方向和 64 位最高位错误。

每一行的 64 个 BIT 非 NULL 时总长为 `8×(1+2+...+8)=288` 字节，加主键和系统字段 17 字节共 305。可空列 64 个，NULL 位图八字节，记录头五字节；非 NULL 记录总长 318。69 行真实形成 level=1 的根和两个叶子，没有通过减少样本强制单页。

id=0 位于页 5，origin=133、End=150，数据部分只有 17 字节；其元信息是八个 `ff` 后跟记录头 `00 00 10 00 1e`。id=2 同在页 5，origin=481、End=786，BIT 数据从 498 开始。这个 NULL/最大值长度对照能发现错误地为 NULL 读取字段字节的实现。

## 混合行的 NULL 与长度元信息

`bit_mixed` 共 11 个可空列，因此位图两字节。id=1 的 Start=188、origin=198，元信息为：

```text
14 c0 0d | 00 00 | 00 00 18 00 58
长度数组   NULL位图    记录头
```

逆向读取长度：note 的 `0d` 表示 13 个 UTF-8 字节；body 的 `c0 14` 表示 external，记录只存 20 字节引用。九个 BIT 都是定长，不消耗长度数组项。值区总长为 `17+29+13+20=79`；上一章列出每个 BIT 的精确偏移。

id=2 交替 NULL，位图（按内存从低地址到高地址）是 `01 55`，检查解码跨越第八个 nullable 列后的定位。id=3 九个 BIT 和 body 为 NULL，note 非 NULL，位图是 `05 ff`；其数据只有 17+13=30 字节。全部 BIT 为 NULL 并不意味着整行不存在。

整树测试仍使用 BIGINT 主键，不把 BIT 作为导航键。`bit_tree` 的 BIT(64) 为 `2^64-1-id`，以高值覆盖有符号溢出及浮点精度错误；同时存在短 BIT 的 NULL，确保列定位不会在跨页时累积偏移。

## SQL 预期与损坏测试

生成器使用 `JSON_ARRAY(...,CAST(bit_column AS UNSIGNED),...)` 导出数值。不能直接把客户端显示的 BIT 字节当作十进制字符串；SQL 的显式数值转换提供独立预期。Python 保留任意精度整数，Go 测试与 JSON 对照使用 `UseNumber`；示例程序也逐行验证最大 uint64。

[bit_test.go](../../bit_test.go) 包含：

- 全部位宽、逐位置位、uint64 返回类型、NULL/零和最大值；真实文件 SHA256 与全部 SQL 行。
- 非法位宽（缺省零、负数、65）、跨类型 bit_length、unsigned/时间/小数/长度属性、BIT 主键拒绝。
- 所有非整字节位宽的未使用高位破坏：返回 ErrCorrupt，整表无部分结果。
- 错误字段字节长度、堆顶截断、输入不被解码器修改，以及混合页 fuzz。

测试只修改内存副本；新资产通过 MySQL `innochecksum`。解析路径目前仍不执行 CRC，不将官方离线校验写成运行时能力。

## 复现与环境说明

本轮原测试 socket 不存在，未启动原实例。使用同一 MySQL 8.0.45 程序，在 `/tmp/innodb-bit-stage13` 初始化独立临时实例，关闭 TCP 网络和 MySQL X，仅本地 socket；没有改原数据目录或配置。最终专用库为 `innodb_reader_fixture_43ee7f373b02`。验证完成后关闭临时服务，项目内夹具可永久离线使用。

生成器在同一持久会话中设置 `STRICT_TRANS_TABLES`，建独立库、插入，执行 FOR EXPORT 后导出 SQL 预期和复制文件，最后 UNLOCK。`generate.sql.gz` 记录 DDL/DML；manifest 记录实际版本、模式、路径、根页、行数和哈希。首轮用于确认宽表层级的临时快照不是最终资产。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzBit$' -fuzztime=10s -parallel=2

gzip -dc testdata/bits/bit_mixed.ibd.gz > /tmp/bit_mixed.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/bit_mixed.ibd testdata/bits/bit_mixed.json
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/bit_mixed.ibd
```

重新生成需要一个正在运行的同版本测试实例，将 SOCKET 换成其路径，并在需要时通过环境 MYSQL_PWD 提供密码：

```sh
python3 scripts/generate_fixtures.py --bits \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket SOCKET --out /tmp/new-bit-fixtures
```

输出目录必须不存在；生成器只新建独立库，不删除原数据。重建后的 space/index ID、事务字段和哈希可以变化，不是文件格式常量。

## 本轮验证结果（2026-09-14）

三组共 673 行 SQL 对照及示例程序输出通过，三个文件通过 SHA256/innochecksum；test/race/vet 通过，核心包覆盖率 97.7%。FuzzBit 独立重跑 10 秒完成 176738 次执行，无失败。首次与其他检查并行的 fuzz 在计时结束时报告 context deadline exceeded，未报告失败输入，重跑已通过。

累计 93 组成功资产、23994 行，另有一组 16 MiB+1 超限拒绝资产，共 94 组。位宽与 NULL/LOB 的真实偏移以及本地文档链接已核对。当前支持边界仍以 [需求](../REQUIREMENTS.md) 为准，元数据自动发现和 BIT 主键未实现。
