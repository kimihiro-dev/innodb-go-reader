# 32 BINARY：全长度与混合记录验证

本章在 [固定二进制布局](31-binary-encoding.md) 上建立验证闭环：确认没有删掉真实字节，也没有误用变长长度数组；区分补零、空值和 NULL，并检查跨页读取。

## 为什么使用十八个真实表

[manifest](../../testdata/fixed_binary/manifest.json) 包含 18 个文件、700 行，所有声明长度 1..255 都在真实 MySQL 中建表并写入，不只依赖人工拼接字节。

| 表 | 数量 | 行数 | 覆盖 |
|---|---:|---:|---|
| binary_widths_1、17、…、241 | 16 | 96 | 每组最多 16 列，覆盖全部 255 种长度 |
| binary_mixed | 1 | 4 | 九种固定宽度、VARBINARY、跨字节位图、主键重排、页外 TEXT |
| binary_tree | 1 | 600 | BINARY(255)/BINARY(3)、NULL、长文本、打乱插入后整树扫描 |
| 合计 | 18 | 700 | SQL HEX 全行逐列对照 |

每个长度组都有六种输入：NULL、空字节串、单字节 `61`、满长变化字节、满长 `20`、以 `ff 00` 开始的短值（不超声明宽度）。它们分别检查字段缺席、全零补齐、右侧补零、任意字节、尾空格保留、内嵌零与无效 UTF-8。

按 16 个长度一组避免一张表的固定行过大。生成器接受这些长度组因实际行大小形成的不同树层级；另有 `binary_tree` 明确要求根 level=1，用于固定的跨页验收。树表的长二进制字段存入主键对应的两个大端字节后补零，这只是测试载荷编码，不是 BINARY 类型要求的字节序。

## 真实 NULL 位图和长度数组

`binary_mixed` 共十二个可空列，NULL 位图两字节。id=1 在页 4 的元信息 [640,651) 为：

```text
14 c0 0a 02 | 00 00 | 00 00 18 02 1d
  长度数组    NULL位图      记录头
```

从右向左读长度数组：VARBINARY 为 `02`，note 为 `0a`（十字节），body 的 `c0 14` 表示页外二十字节引用。九个 BINARY 都没有对应的长度项。它们的数据范围在上一章表格中；本地 body 引用再恢复成 60000 字节 TEXT。

id=0 的 VARBINARY 是空值，占一个值为零的长度项，但不占值区字节。BINARY(3) 同行插入 X'61' 后仍占三字节。id=2 的 varying 为 NULL，连长度项也不存在；id=3 的九个 BINARY 和 body 都为 NULL，而 varying 为空、note 非空。

| id | origin | End | 数据区长度 | 位图（低地址到高地址） |
|---:|---:|---:|---:|---|
| 0 | 130 | 640 | 510 | `00 00` |
| 1 | 651 | 1183 | 532 | `00 00` |
| 2 | 1192 | 1372 | 180 | `03 55` |
| 3 | 1381 | 1408 | 27 | `09 ff` |

id=3 只有 17 字节主键/系统字段与十字节 note；空 VARBINARY 返回非 nil 的零长度切片。NULL 的 BINARY 返回 nil，全零 BINARY 返回声明长度的 []byte。三者不能混淆。

## SQL 对照与内存所有权

SQL 预期由同一导出锁会话的 `HEX(column)` 生成，存入 `.expected.json.gz`，不会由解析器生成答案。测试对解析器 []byte 编码 HEX，与 SQL 字符串逐字节比较；其他列继续使用精确 JSON 对照。示例程序输出 Base64，另行解码成字节后核对同一 SQL HEX。

`TestFixedBinaryFixtures` 检查所有 255 种宽度、固定行长、NULL、补零和空格；修改返回的一个字节后确认输入文件数据未被修改，以验证返回切片的所有权。`TestFixedBinaryContracts` 拒绝零/负/256 字节宽度、错误类型参数和 BINARY 主键，并确认它不参加变长长度数组。

`TestFixedBinaryDamage` 在内存副本中改动堆顶制造截断，要求 ErrCorrupt 且不返回部分结果；另将合法固定字段字节改成 `00/20/80/ff`，要求原样还原。任意二进制都可能是有效值，不能为提高“损坏覆盖”而引入错误的 UTF-8 或尾零校验。[测试入口](../../fixed_binary_test.go) 同时包含混合记录 fuzz。

## 环境与重建

最终数据库为 `innodb_reader_fixture_3b57ac397814`。原测试 socket 不存在，本轮启动上一阶段的 `/tmp/innodb-bit-stage13` 独立 MySQL 8.0.45，仅本地 socket、关闭网络和 MySQL X，未修改原实例。验证后关闭临时服务；交付的夹具可离线复跑。

生成器仅设置会话 STRICT_TRANS_TABLES；DDL/DML 保存为 `generate.sql.gz`。同一持久连接执行 FOR EXPORT、SQL SELECT 和文件复制后才 UNLOCK；manifest 记录版本、配置、根页及解压后 SHA256。gzip 是文件交付压缩，与 InnoDB 页压缩无关。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzFixedBinary$' -fuzztime=10s -parallel=2

gzip -dc testdata/fixed_binary/binary_mixed.ibd.gz > /tmp/binary_mixed.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/binary_mixed.ibd testdata/fixed_binary/binary_mixed.json
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/binary_mixed.ibd
```

重建需要正在运行的同版本实例，将 SOCKET 替换成其 socket 路径；需要密码时预先通过环境 MYSQL_PWD 提供：

```sh
python3 scripts/generate_fixtures.py --fixed-binary \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket SOCKET --out /tmp/new-fixed-binary-fixtures
```

目录必须不存在，每次生成独立库，不删除已有库。重建后页号、space/index ID、LSN 和哈希可能变化；学习时应区分格式规则与某份快照的具体数字。

## 验证结果（2026-09-14）

18 组共 700 行 SQL HEX 对照及示例 Base64 解码对照通过；18 个物理文件通过 SHA256 与官方 innochecksum。test/race/vet 通过，核心包覆盖率 97.7%；FuzzFixedBinary 设置 10 秒预算，完成 81248 次执行，无失败（含收尾共约 11.6 秒）。实际字节、位图及文档本地链接核对通过。

全项目累计 111 组成功夹具、24694 行，另有一组 16 MiB+1 超限拒绝资产，共 112 组。解析路径仍未验证 CRC；显式元数据仍须可信，不能据此声称读取任意表或识别所有 schema 错配。
