# 36 SET：全成员数、空标签与混合记录验证

本章验证 [SET 位掩码解码](35-set-encoding.md) 在全部成员数、空标签、NULL、混合 ENUM/LOB 和跨页树中的行为。验证对象既包括可读字符串，也包括完整 uint64 物理掩码。

## 四组真实样本

[manifest](../../testdata/sets/manifest.json) 记录四个文件、685 行、MySQL 8.0.45、16 KiB、crc32，以及原始文件 SHA256。所有表为 DYNAMIC、独立非压缩非加密表空间；gzip 仅压缩交付文件。

| 表 | 行数 | 验证内容 |
|---|---:|---|
| set_widths | 69 | 64 个 SET 列，成员数 1..64；全 NULL、空集合、全一、交替位与逐个置位 |
| set_labels | 12 | 空标签首/中/尾位置，中文/emoji/数字样标签/引号/反斜杠，乱序重复输入 |
| set_mixed | 4 | 九种字典、ENUM、两字节位图、主键重排、页外 TEXT |
| set_tree | 600 | 第 64 位始终置位，短 SET/NULL、长文本及根 level=1 多叶树 |
| 合计 | 685 | 字符串与掩码 SQL 双重对照 |

set_widths 的 id=0 为 NULL，id=1 为零集合，id=2 为各列的全一掩码，id=3/4 为 AA/55 交替位。id=5..68 分别设置第 0..63 位，超出该列成员数的位截成零。这样覆盖每个字典成员的位置，不只检查最大值。

所有 SET 非 NULL 时总字节数为 `8×(1+2+3+4)+32×8=336`，加主键/系统字段 17 后是 353 字节。NULL 行只有 17 字节。64 个 nullable SET 使位图恰好八字节，真实表形成 level=1 树；测试逐条核对固定长度。

## 字符串与掩码独立验证

`.expected.json.gz` 来自 SQL CAST AS CHAR，`.masks.json.gz` 来自 CAST AS UNSIGNED，两者均在持有导出锁的同一会话生成。掩码文件只列 SET 列，按逻辑顺序保存；Go 用 *uint64 解码每个预期值，NULL 与零分开，最高位和最大值不经过浮点数。

set_mixed 同时有 ENUM，额外 `.ordinals.json.gz` 独立验证 EnumIndexes，确保两种元信息 map 互不混淆。没有 ENUM 的样本其 ordinal 行为空数组。示例程序只输出 Values 字符串；需要精确选择状态时使用 Read 返回的 SetMasks。

set_labels 的 id=1 是掩码 0，id=2 是掩码 1；首列字典首项为空，两行显示都为空，但 map 值不同。id=8 全选三个成员时，前三列分别为 `a,b`、`a,,b`、`a,b,`。id=10 输入重复和乱序的 `'b,a,a'`，输出按声明顺序显示；解析器不重建插入文本中的重复或顺序。

## 混合记录元信息

set_mixed 共十二个可空列，NULL 位图两字节。id=1 位于页 4，Start=189、origin=199、End=279，元信息为：

```text
14 c0 0a | 00 00 | 00 00 18 00 59
变长长度   NULL位图    五字节记录头
```

反向读取长度数组：note 是 0a（十字节），body 是 c0 14（external 二十字节引用）。SET 与 ENUM 均无长度项；本地 body 引用通过原有 LOB 逻辑恢复 60000 字节 TEXT。完整字段范围见上一章。

id=2 的位图为 `01 55`，隔列 SET 为 NULL，ENUM 仍有效；id=3 的位图为 `0b ff`，SET/ENUM/body 为 NULL、note 非 NULL，因此数据区只有 27 字节，SetMasks/EnumIndexes 都是 nil。位图字节顺序是内存低地址到高地址；文件绝对偏移还需加页基址 65536。

## 损坏与回归

[set_test.go](../../set_test.go) 包含：

- 成员数 1..64、全部物理宽度、逐位置位、空集合/NULL、最高位和空标签分隔规则。
- 缺失/超过 64 项字典、非法 UTF-8、含逗号成员、错误属性、SET 主键拒绝。
- 每个存在未使用位的成员数：将第 N 位强制置位，必须 ErrCorrupt；包括 33..63 项的八字节字段。
- 固定字段字节数不匹配与堆顶截断；失败无部分表结果。
- 混合 ENUM 序号不变，旧表 SetMasks 为 nil；解码不修改输入字节。
- 混合记录有界 fuzz，失败返回 nil 结果。

字典本身属于可信元数据；检查合法掩码不保证字典顺序正确。CRC 仍未进入解析路径，不能将官方离线校验说成库的运行时功能。

## 原实例与复现

最终新独立库为 `innodb_reader_fixture_37ef7488e9e0`。使用用户已重启的原实例，只设置生成会话 STRICT_TRANS_TABLES；全局模式保持 STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION，原服务持续运行。未修改既有用户表。

生成器在同一持久连接完成 FOR EXPORT、字符串/掩码/ENUM 序号 SELECT、文件复制后再 UNLOCK。DDL/DML 与会话设置保存在 generate.sql.gz，manifest 记录环境、页号和哈希。重新生成会改变 space/index ID、事务字段等快照数据。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzSet$' -fuzztime=10s -parallel=2

gzip -dc testdata/sets/set_labels.ibd.gz > /tmp/set_labels.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/set_labels.ibd testdata/sets/set_labels.json
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/set_labels.ibd
```

重建前通过环境设置 MYSQL_PWD，输出目录必须不存在：

```sh
python3 scripts/generate_fixtures.py --sets \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-set-fixtures
```

脚本每次创建独立库，不删除已有数据；离线测试无需 MySQL 运行。当前能力仍限可信 schema 和既定表空间/新建表契约，SET 主键、SQL 比较、自动元数据与历史版本不在范围内。

## 验证结果（2026-09-14）

四组 685 行字符串/掩码双重 SQL 对照、混合 ENUM 序号对照、示例字符串输出全部通过。四个文件通过 SHA256 和官方 innochecksum；test/race/vet 通过，核心包覆盖率 97.9%。FuzzSet 10 秒完成 165906 次执行，无失败。手册真实字节、位图和本地链接已核对。

全项目累计 122 组成功资产、26519 行，另有一组 16 MiB+1 超限拒绝资产，共 123 组。
