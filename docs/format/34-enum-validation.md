# 34. ENUM：双重 SQL 对照、空标签与混合记录

本章验证 [字典解码](33-enum-encoding.md) 不仅返回正确显示值，也保留正确物理序号；同时检查新增定长类型不会破坏 NULL 位图、页外值和整树扫描。

## 七组真实样本

[夹具目录与 manifest](../../testdata/enums/manifest.json) 记录 MySQL 8.0.45、16 KiB、crc32 和各文件哈希。表均为 DYNAMIC、独立非压缩非加密表空间。

| 表 | 行数 | 主要覆盖 |
|---|---:|---|
| enum_1 | 3 | 最小字典，NULL/0/1 |
| enum_255 | 257 | 一字节字典上限，NULL 及全部 0..255 |
| enum_256 | 258 | 两字节宽度起点，NULL 及全部 0..256 |
| enum_65535 | 8 | 最大字典，255/256、32767/32768、65535 |
| enum_labels | 10 | NULL、零号、合法空标签、中文/emoji、数字样标签、引号/反斜杠/逗号 |
| enum_mixed | 4 | 一/两字节混合、主键重排、跨字节位图和 60000 字节 TEXT |
| enum_tree | 600 | 打乱插入、两种字典、NULL/0、长文本及 level=1 跨叶扫描 |
| 合计 | 1140 | 标签和序号独立 SQL 对照 |

大字典确实在原测试实例上建表成功，不是仅由单元测试模拟的元数据。生成文件包含字典和 SHOW CREATE TABLE，字典资产较大，但行内值仍只占两字节。

## 两份独立预期

每张表保存 `.expected.json.gz` 标签行和 `.ordinals.json.gz` 枚举序号行：

- 标签使用 `CAST(enum_column AS CHAR)`，与 Record.Values 对照。
- 序号使用 `CAST(enum_column AS UNSIGNED)`，与 Record.EnumIndexes 对照。
- 序号文件只含 ENUM 列，按 schema 中的逻辑顺序排列；NULL 仍保存为 JSON null。

`enum_labels` 的 id=1/2 显示标签都是空字符串，但实际字节分别为 00/01，字段页内偏移分别为 166/190。id=0 为 NULL，不占字段字节、EnumIndexes 无该列键。只比较标签会漏掉这项错误，因此测试同时比较数值序号。

字典第 4 项是标签 `"2"`。id=3 用数值 2 插入，得到第 2 项 ready；id=9 用 SQL 字符串 `'2'` 插入，得到第 4 项，物理值为 04。这是插入语义对存储结果的影响；解析器只读取最终序号，不重新执行 SQL 转换规则。

## 混合行定位

`enum_mixed` 有九个可空 ENUM，偶数编号字典 7 项、一字节，奇数编号字典 256 项、两字节；后接 VARCHAR 和 TEXT，共 11 个可空列，位图两字节。

id=1 页号 4，Start=169、origin=179、End=239，元信息是：

```text
14 c0 0a | 00 00 | 00 00 18 00 45
变长长度   NULL位图    五字节记录头
```

长度逆向读出 note=10 字节、body=20 字节外部引用。ENUM 没有长度数组项。数据布局如下：

| 页内区间 | 相对 origin | 内容 |
|---|---:|---|
| [179,196) | 0 | INT id + 两个系统字段，共 17 字节 |
| [196,197) | 17 | e0：03 → 中文😀 |
| [197,199) | 18 | e1：01 00 → v256 |
| [199,200) | 20 | e2：03 |
| [200,202) | 21 | e3：01 00 |
| [202,203) | 23 | e4：03 |
| [203,205) | 24 | e5：01 00 |
| [205,206) | 26 | e6：03 |
| [206,208) | 27 | e7：01 00 |
| [208,209) | 29 | e8：03 |
| [209,219) | 30 | note：UTF-8 “枚举😀” |
| [219,239) | 40 | body：20 字节引用，恢复为 60000 字节 TEXT |

九个 ENUM 总长 13，整条记录数据区 `17+13+10+20=60`。页基址为 65536，例如 e1 文件偏移为 `65536+197=65733`。逻辑 id 插在前四个 ENUM 之后，但物理主键提前，因此 EnumIndexes 的键必须用逻辑列下标；e4 的键是 5，不是 4。

id=0 九个序号均零；id=2 交替 NULL，位图为 `01 55`（低地址到高地址）；id=3 所有 ENUM 和 body 为 NULL、note 保留，位图为 `05 ff`，EnumIndexes 为 nil。实际行长和返回 map 键数都参与测试。

## 损坏、兼容性与复现

[enum_test.go](../../enum_test.go) 覆盖范围检查、错误字节长度、字典非法/过长/UTF-8 错误、跨类型字典属性、ENUM 主键拒绝、序号越界和堆顶截断。失败要求 ErrUnsupported 或 ErrCorrupt，并且无部分表结果。字典 1 项的 ff、字典 256 项的 ffff 均被拒绝；65535 项的 ffff 则是有效最高序号。

旧 fixture 的 Record.EnumIndexes 仍为 nil，既有 Values 类型不变。范围合法不意味着外部字典必然正确：换成同样成员数的错误字典仍可能产生错误标签，这需要后续元数据发现解决。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzEnum$' -fuzztime=10s -parallel=2

gzip -dc testdata/enums/enum_labels.ibd.gz > /tmp/enum_labels.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  /tmp/enum_labels.ibd testdata/enums/enum_labels.json
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum /tmp/enum_labels.ibd
```

重新生成前在环境设置 MYSQL_PWD，输出目录必须不存在：

```sh
python3 scripts/generate_fixtures.py --enums \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-enum-fixtures
```

本轮使用已重启的原测试实例，最终独立库为 `innodb_reader_fixture_0d4169df4215`。仅生成会话清空 sql_mode，以允许零号错误值；全局模式在前后均为 STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION。首次模式顺序探测失败所建的独立库保留，最终快照来自后续新库；未改既有用户表，也未关闭原实例。

DDL/DML 和会话模式保存于 generate.sql.gz；同一持久连接保持 FOR EXPORT 锁，执行标签/序号 SELECT 并复制物理文件后才 UNLOCK。gzip 只用于交付压缩；SHA256 对解压文件计算。重建后的页号、space/index ID、事务字段与哈希允许变化。

## 验证结果（2026-09-14）

七组 1140 行标签与序号 SQL 对照、示例标签输出、SHA256 和七个文件官方 innochecksum 全部通过。test/race/vet 通过，核心包覆盖率 97.8%；FuzzEnum 10 秒完成 174081 次执行，无失败。手册字节、偏移和本地链接已核对。

累计 118 组成功资产、25834 行，另有一组 16 MiB+1 超限拒绝资产，共 119 组。读取路径仍未执行 CRC 校验；ENUM 字典和其他 schema 仍由调用方可信提供，不包含自动元数据发现或历史事务可见性。
