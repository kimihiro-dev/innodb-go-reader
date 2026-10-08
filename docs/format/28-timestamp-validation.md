# 28 TIMESTAMP：时区实验、混合记录与验证

本章在 [四字节秒数解码](27-timestamp-encoding.md) 的基础上回答：如何证明结果是正确的 UTC 时间点，而不只是看起来像日期的字符串？如何保证新定长字段没有破坏 NULL、LOB 和整树扫描？

## 三个插入时区与三个读取时区

`timestamp_zones` 包含 INT 主键、TIMESTAMP(6) 的 `stamp` 和 DATETIME(6) 的 `wall`。三行都插入相同文本 `2024-02-29 12:34:56.123456`，但逐行设置会话 `time_zone`：

| id | 插入时区 | 解析器 UTC stamp | wall（所有读取时区相同） |
|---:|---|---|---|
| 0 | +00:00 | 2024-02-29 12:34:56.123456 | 2024-02-29 12:34:56.123456 |
| 1 | +08:00 | 2024-02-29 04:34:56.123456 | 2024-02-29 12:34:56.123456 |
| 2 | -05:30 | 2024-02-29 18:04:56.123456 | 2024-02-29 12:34:56.123456 |

这是 SQL 实测结果，不是解析器生成的预期。生成器随后在同一锁定快照上分别切换三个读取时区，保存 [显示预期](../../testdata/timestamps/timestamp_zones.zones.json.gz)。例如 id=0 在 +08:00 显示 `20:34:56.123456`，在 -05:30 显示 `07:04:56.123456`；物理文件没有随显示时区改变。

```text
插入文本 + 插入会话时区 → UTC 时间点 → TIMESTAMP 物理秒数
                                             ↓
                    解析器：固定 UTC字符串 ← 解码
                    MySQL：按读取会话时区显示
```

主预期文件统一使用 `+00:00`，另存的三组显示值用于独立核查偏移。`TestTimestampZones` 对每个时间点应用固定偏移并核对 SQL 输出，同时核对 DATETIME 不变。命名时区、夏令时歧义、原会话恢复不是本阶段范围。

## 混合记录：定长字段不会消耗变长长度

`timestamp_mixed` 有九个 TIMESTAMP，fsp 按 0..6 循环；逻辑 id 列在中间，物理聚簇记录仍先放 id，再放系统字段和其他列。后面还有 DATETIME(6)、VARCHAR、TEXT。共 12 个可空列，位图占两字节。

id=1 的页号为 4，Start=213、origin=223、End=327，元信息为：

```text
14 c0 0a | 00 00 | 00 00 18 00 71
变长长度   NULL位图    五字节记录头
```

长度数组逆向读取：VARCHAR 的长度是 `0a`（“时间😀”的 UTF-8 十字节）；TEXT 的 `c0 14` 标记页外、此处仅存 20 字节引用。不要把前面某个 TIMESTAMP 的小数长度塞入这段数组。

| 页内区间 | 内容与宽度 |
|---|---|
| [223,240) | id 4 + DB_TRX_ID 6 + DB_ROLL_PTR 7 |
| [240,244) | t0，fsp=0，4 字节 |
| [244,249) | t1，fsp=1，5 字节 |
| [249,254) | t2，fsp=2，5 字节 |
| [254,260) | t3，fsp=3，6 字节 |
| [260,266) | t4，fsp=4，6 字节 |
| [266,273) | t5，fsp=5，7 字节 |
| [273,280) | t6，fsp=6，7 字节 |
| [280,284) | t7，fsp=0，4 字节 |
| [284,289) | t8，fsp=1，5 字节 |
| [289,297) | wall，DATETIME(6)，8 字节 |
| [297,307) | note，UTF-8 文本 10 字节 |
| [307,327) | body，20 字节外部引用，恢复成 60000 字节 TEXT |

因此数据区长度 `17+49+8+10+20=104`。文件绝对偏移需再加页基址 65536。id=2 交替 NULL，id=3 大部分列为 NULL，用于检查位图跨字节后的字段定位。600 行 `timestamp_tree` 则验证根 level=1 的多叶扫描；TIMESTAMP 只在叶子解码，导航键仍为 BIGINT。

## 夹具与覆盖

[manifest](../../testdata/timestamps/manifest.json) 记录 MySQL 8.0.45、16 KiB、DYNAMIC、crc32、每个文件的 SHA256、页号和行数。专用数据库为 `innodb_reader_fixture_0a36f4d2b0f5`。

| 样本 | 表数 | 行数 | 验证内容 |
|---|---:|---:|---|
| timestamp_fsp_0..6 | 7 | 84 | 全精度、NULL/零、最小正常秒、2038 端点、尾零、步长、最大小数、跨日舍入 |
| timestamp_mixed | 1 | 4 | 两字节位图、主键重排、DATETIME/VARCHAR/LOB |
| timestamp_tree | 1 | 600 | 打乱插入、跨叶扫描、两种精度与 NULL |
| timestamp_zones | 1 | 3 | 三插入时区、三显示时区与 DATETIME 对照 |
| 合计 | 10 | 691 | 全行逐列 SQL 对照 |

输入 `2024-02-29 23:59:59.999999` 在 fsp=0..5 存成次日零点，在 fsp=6 保持原值；解析器读取已经舍入后的物理值，不重复进行 SQL 舍入。实测支持 fsp=6 的 `2038-01-19 03:14:07.999999`，对应 `7f ff ff ff 0f 42 3f`。

生成会话只设置 `STRICT_TRANS_TABLES`（不含 NO_ZERO_DATE）和显式时区；不会修改全局变量。生成 SQL 保存在 `generate.sql.gz`。所有表在同一持久连接执行 `FLUSH TABLES ... FOR EXPORT` 后，进行 SELECT 和文件复制，最后 UNLOCK；没有在复制前断开持锁连接。压缩仅用于交付文件，不是 InnoDB 压缩表空间。

## 测试与复现

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzTimestamp$' -fuzztime=10s -parallel=2

gzip -dc testdata/timestamps/timestamp_zones.ibd.gz > /tmp/timestamp_zones.ibd
TZ=Asia/Shanghai GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/timestamp_zones.ibd testdata/timestamps/timestamp_zones.json
```

重新生成时，先在环境中设置 `MYSQL_PWD`，输出目录必须不存在：

```sh
python3 scripts/generate_fixtures.py --timestamps \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-timestamp-fixtures
```

每次创建独立库，不删除已有数据。重建后 space/index ID、事务信息和文件哈希可能改变。官方校验示例：

```sh
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/timestamp_zones.ibd
```

2026-09-11 验证结果：

- test/race/vet 通过；核心包语句覆盖率 **97.7%**。
- FuzzTimestamp 10 秒完成 **169604** 次执行，无失败。
- 十个新 `.ibd` 通过 SHA256 校验和官方 innochecksum；读取路径仍未执行 CRC 校验。
- 示例程序的全部 691 行在 `TZ=UTC`、`Asia/Shanghai`、`America/New_York` 下分别与 UTC SQL 预期一致。
- 全项目累计 **90 组成功夹具、23321 行**，另有一组 16 MiB+1 超限拒绝资产，共 91 组资产。

[测试代码](../../timestamp_test.go) 还验证：错误字节长度、schema 非法属性、时间类型主键拒绝、秒数 `0x80000000/0xffffffff`、零秒带非零小数、小数容量溢出、奇数精度不对齐及堆顶截断；损坏返回 ErrCorrupt 且无部分结果。正常格式化不改输入字节。

这些检查不保证识别所有错误外部 schema。例如两种精度占相同宽度时，某些值可能同时合法；自动元数据发现仍未实现。当前范围仍是可信 schema、新建只插入的 MySQL 8.0.45 简单表，不包含旧时间格式、历史事务版本或时间类型主键。
