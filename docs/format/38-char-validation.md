# 38 CHAR：全长度、页外值与验证

本章在 [CHAR 存储规则](37-char-encoding.md) 基础上，学习如何区分本地引用长度与完整字段长度，并验证所有 255 种声明长度、NULL、空格和跨页树。

## 35 组真实夹具

[manifest](../../testdata/chars/manifest.json) 保存 MySQL 8.0.45、16 KiB、crc32、DYNAMIC、行数及解压后 SHA256。gzip 只是交付压缩，表空间没有使用压缩页。

| 夹具 | 数量 | 行数 | 覆盖 |
|---|---:|---:|---|
| char_widths_1、9、…、249 | 32 | 320 | CHAR(1..255)，每表最多八个长度、十行 |
| char_mixed | 1 | 4 | 九种长度、两字节位图、主键重排、VARCHAR 尾空格和页外 TEXT |
| char_external | 1 | 2 | 十二列 CHAR(255)，五个实际页外字段、全 NULL 行 |
| char_tree | 1 | 600 | 中文、NULL、乱序插入后的 level=1 多叶树 |
| 合计 | 35 | 926 | SQL 显示与物理文本核对 |

每个长度表包含 NULL、空字符串、a、满长中文、满长 emoji、纯空格、满长 NBSP、tab、NUL、带尾空格的 a（N=1 时为空格）。特殊文本通过 UTF-8 十六进制 CONVERT 生成，避免脚本或 SQL 转义改变 NUL。

`.expected.json.gz` 独立来自 SQL CAST AS CHAR，Go 测试先对照全部 Values。随后按本章明确的存储契约，从 SQL 文本补到至少 N 字节，与 CharStorage 对照；这部分是格式规则推导的预期，不宣称 SQL 直接导出了物理空格。上一章的原始字节提供额外直接核查。

## 二十字节引用怎样恢复 CHAR

char_external 的 id=0 含十二列 CHAR(255)，每列均为 255 个 emoji，完整字段长度为 `255×4=1020` 字节。受整行页内容量限制，其中五列实际转为页外存储。

```text
聚簇页 4：主键/事务字段 → 五个 20 字节引用 → 七个 1020 字节文本
                              ↓
                 LOB_FIRST 页 5、6、7、8、9
                              ↓
                 每列恢复 1020 字节，再验证 CHAR
```

该记录 Start=120、origin=151、End=7408，本地数据长度为 `17+5×20+7×1020=7257`。记录前长度数组按低地址到高地址包含七组 `fc 83`、五组 `14 c0`，其后为位图 `00 00`、记录头 `00 00 10 1c 60`。反向解码时，`83 fc` 表示 1020 字节，`c0 14` 表示 external 二十字节引用。

五个引用的页内起点依次为 168、188、208、228、248，对应逻辑列 1..5。第一个引用的真实字节是：

```text
00 00 00 c1 | 00 00 00 05 | 00 00 00 01 | 00 00 00 00 00 00 03 fc
  space ID      first page     version          flags / length
```

所有整数以大端读取；space ID=193、首个 LOB 页=5、版本=1、完整长度=1020。该样本首个活动索引项在页 5 偏移 96，数据从页 5 偏移 696 开始，长度 1020。其余四列首个页为 6..9，索引/数据偏移和长度相同。记录引用的文件起点为 `65536+168=65704`；LOB 数据起点为 `5×16384+696=82616`。

二十字节是引用宽度，不能按 CHAR(255) 的最小 255 字节规则校验引用本身。现有 [lob.go](../../lob.go) 完成引用、版本、索引链及数据拼接检查，再调用文本校验。还原后 Values 与 CharStorage 均为 255 个 emoji，Record.External 仍保存引用与来源块。

id=1 的十二列全 NULL：Start=7408、origin=7415、End=7432，元信息为 `0f ff | 00 00 18 e3 79`。没有长度项或引用，数据仅含 17 字节主键/事务字段，CharStorage 为 nil。

## 位图与错误输入

char_mixed 有十一个可空列，位图占两字节。id=2 的位图为 `01 55`，隔列 CHAR 为 NULL；id=3 位图为 `05 ff`，全部 CHAR 与 body 为 NULL，varying 非 NULL。后者 origin=2043、End=2064，只剩 `17+4=21` 字节数据，没有 CharStorage 项。

[char_test.go](../../char_test.go) 验证全部 N、schema 错误参数、CHAR 主键拒绝、UTF-8/字符上限/物理最小长度/非规范尾空格、堆顶截断、原有表元信息不变及整树扫描。损坏真实 char_mixed 字段时返回 ErrCorrupt 和 nil 结果。FuzzChar 对真实页外夹具进行有界变异，检查解析不会产生崩溃或失败后的部分结果。

外部 schema 仍是可信输入：长度检查不能证明一个合法但错误的列定义与文件匹配。CRC 也尚未加入库读取路径；下面的 innochecksum 是对交付资产的独立验证。

## 复现

新独立库为 `innodb_reader_fixture_eee9ec340d1e`，使用原测试实例。生成器仅对生成会话设置 STRICT_TRANS_TABLES；全局模式仍为 STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION，服务保持运行。

生成器在持久连接持有 FOR EXPORT 锁时导出 SQL 预期和复制文件，最后 UNLOCK；生成 SQL 与会话配置保存于 generate.sql.gz。重建会改变 space/index ID、LSN 和事务字段，因此样本数值不是格式常量。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzChar$' -fuzztime=10s -parallel=2

gzip -dc testdata/chars/char_mixed.ibd.gz > /tmp/char_mixed.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/char_mixed.ibd testdata/chars/char_mixed.json
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/char_mixed.ibd
```

示例打印 Values；查看物理空格需使用 Read 返回的 CharStorage。重新生成前通过环境设置 MYSQL_PWD，输出目录必须不存在：

```sh
python3 scripts/generate_fixtures.py --chars \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-char-fixtures
```

## 验证结果（2026-09-14）

35 组 926 行全部通过 SQL 对照、CharStorage 核对与示例输出对照；255 种声明长度、五个实际页外 CHAR、混合 VARCHAR/TEXT 和 600 行树均通过。35 个文件通过 SHA256 与官方 innochecksum；test/race/vet 通过，核心包覆盖率 97.9%。FuzzChar 10 秒预算完成 383481 次执行，无失败。文档中的真实字节/偏移、本地链接、阶段锚点与生成器语法已核对。

累计 157 组成功资产、27445 行，另有一组 16 MiB+1 超限拒绝资产，共 158 组。仅新增专用测试库，未修改既有用户表或全局配置，未关闭原实例。
