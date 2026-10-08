# 50. 零长度字段、跨页字符与字符集验收

<a id="50零长度字段跨页字符与字符集验收"></a>

前章说明编码和 CHAR 布局，本章关注容易漏掉的零长度字段、跨 LOB 块字符，以及怎样证明解码器没有吞掉字节或替换字符。

<a id="1-长度为零不等于-null也不等于没有长度元数据"></a>

## 长度为零不等于 NULL，也不等于没有长度元数据

真实新建表支持 CHAR(0)、BINARY(0)、VARCHAR(0)、VARBINARY(0)。这四种列的非 NULL 值没有载荷字节，但本版本的记录中仍各消费一个值为零的长度元数据字节。

源码判断依据是索引字段的 `fixed_len`。单字节 CHAR(N>0)、BINARY(N>0) 有非零固定宽度；宽度为零时 `fixed_len=0`，进入变长路径。原实现使用“最大字节数是否为零”判断是否有长度数组，不能覆盖这一边界，因此新增 `Column.isVariable()` 分离这两个问题。

```text
NULL 列：NULL 位=1 → 不读取载荷，不消费该列变长长度
零宽非 NULL 列：NULL 位=0 → 消费长度 00，读取零字节载荷
```

[charset_latin1_bin](../../testdata/charset/charset_latin1_bin.ibd.gz) 中：

- id=1：c/v/t、四个零宽列及字典列全部 NULL；note 有值。
- id=2：非 NULL 空值；CHAR(5) 存五个空格，四个零宽列无载荷，但有四个长度零。

id=2 位于页 4，Start=158、origin=172、End=209，实际元数据：

```text
0d 00 00 00 00 00 00 | 00 00 | 00 00 18 00 33
```

从右向左读长度：v=0、t=0、cz=0、bz=0、vz=0、vbz=0、note=13。若漏掉任何一个零宽长度字节，Start 和页内占用核算就会偏移。

| 列 | 非 NULL 空值的 Values | 辅助存储证据 |
|---|---|---|
| CHAR(0) | `""` | CharStorage 有空字符串项；TextBytes 有非 nil 空切片 |
| VARCHAR(0) | `""` | TextBytes 有非 nil 空切片 |
| BINARY(0)/VARBINARY(0) | 非 nil `[]byte{}` | JSON 示例输出 Base64 空字符串 |
| 任意上述列为 SQL NULL | nil | TextBytes/CharStorage 中没有该列项 |

查询辅助 map 时要用 `value, ok := map[index]`，不要仅根据空字符串或长度零判断 NULL。原始字节与调用方输入缓冲独立，后续修改文件缓冲不会改变返回的 TextBytes。

<a id="2-utf-8-字符可以跨-lob-块"></a>

## UTF-8 字符可以跨 LOB 块

[external_utf8mb3](../../testdata/charset/external_utf8mb3.ibd.gz) 的 id=2 保存 TINYTEXT、MEDIUMTEXT、LONGTEXT 三列“中”的重复文本。MEDIUMTEXT 是 20000 个字符、60000 字节；LONGTEXT 是 21000 个字符、63000 字节。

记录在页 4，origin=169。TINYTEXT 先占 120 字节，随后 MEDIUMTEXT 的 20 字节引用在页内 306：

```text
00 00 00 e0 00 00 00 09 00 00 00 01 00 00 00 00 00 00 ea 60
space=224    first=9      version=1    总长度=60000
```

其实际活动块：

| LOB 页 | 页内起点 | 字节数 |
|---:|---:|---:|
| 9 | 696 | 15680 |
| 10 | 49 | 16327 |
| 11 | 49 | 16327 |
| 12 | 49 | 11666 |

“中”的编码为 `e4 b8 ad`，而 `15680 % 3 = 2`。第一块最后两个字节是 `e4 b8`，第二块第一个字节是 `ad`。逐块做 UTF-8 验证会误报截断；正确顺序是：验证 LOB 链与完整长度，拼接字节，验证/转换字符集，保存 TextBytes，生成字符串。

第一块文件起点 `9×16384+696=148152`，第二块起点 `10×16384+49=163889`。这些是文件绝对偏移，表中的 696/49 是页内偏移，TextBytes 则是没有物理页间隙的逻辑字节流。

四个字符集的页外夹具共八个真实页外字段；ASCII 与 latin1 同样沿用完整 LOB 路径。TextBytes 保存拼接后的全部原始数据，物理块来源仍在 Record.External，二者职责不同。

<a id="3-夹具矩阵与-sql-预期"></a>

## 夹具矩阵与 SQL 预期

本轮在原测试实例新建专用库 `innodb_reader_charset_c3a4f995f69a`，共 21 个快照、1064 行，完整清单见 [manifest.json](../../testdata/charset/manifest.json)。

| 类别 | 快照数 | 行数 | 覆盖 |
|---|---:|---:|---|
| 九个 collation | 9 | 54 | NULL/空/尾空格/制表符/特殊字符、零宽列、ENUM/SET |
| widths_* | 4 | 16 | CHAR 0/1/63/64/85/86/127/128/255；VARCHAR 另含256 |
| external_* | 4 | 8 | 四字符集、TINY/MEDIUM/LONGTEXT、八个页外字段 |
| bytes_ascii、bytes_latin1 | 2 | 384 | ASCII全部128字节，latin1全部256字节 |
| charset_utf8_alias | 1 | 2 | SQL utf8 别名到 utf8mb3 元数据 |
| charset_tree | 1 | 600 | latin1混合CHAR/VARCHAR/TEXT，20个聚簇页 |

字符宽度矩阵分别用空值、满宽 ASCII、满宽多字节或高位单字节数据测试，覆盖 255/256 最大字节数以及 127/128 实际长度规则。utf8mb3 非法序列和补充平面拒绝另用合成测试验证，不伪称数据库生成了非法文本。

SQL 预期包含：

- `HEX(column)`：正常 SQL 显示对应的原编码字节。
- `CONVERT(column USING utf8mb4)`：独立的标准文本输出。
- `CHAR_LENGTH(column)`：字符数；不等于 Go 字节数。
- ENUM/SET 的数值：与 EnumIndexes/SetMasks 对照，本批共90次检查。
- 在生成会话临时启用 `PAD_CHAR_TO_FULL_LENGTH` 后再次输出 HEX/文本：取得 CHAR 的完整 SQL 补空格视图。

`HEX(CHAR列)` 默认会受到 SQL 去尾空格影响，不能直接把这个结果称为原始物理字节。测试将实际 CharStorage 补足到 N 个字符，再与独立的完整 SQL 视图比较；同时用布局规则验证物理长度，并在第49章展示直接提取的实际字节。VARCHAR/TEXT 的 TextBytes 可直接与正常 HEX 逐字节比较。

生成器 [generate_charset_fixtures.py](../../scripts/generate_charset_fixtures.py) 记录实际 SQL、SHOW CREATE TABLE、SQL 索引元信息、版本和配置。每张表保持 `FLUSH TABLES ... FOR EXPORT` 锁期间查询和复制，再在同一连接解锁。只设置生成会话 sql_mode/time_zone；未改既有表、全局配置或停止实例。密码仅使用 MYSQL_PWD 环境变量。

<a id="4-损坏和兼容性测试"></a>

## 损坏和兼容性测试

[charset_test.go](../../charset_test.go) 验证自动/手工 schema、完整读取结果、真实 SQL 预期、原始字节及 SHA256。SQL 数值预期用精确数值方式比较，不通过 float64 中转序号或掩码。

合成检查包含 ASCII 高位字节、非法/截断/过长 UTF-8、代理项、超出 Unicode 最大值、utf8mb3 四字节字符；MySQL latin1 的五个兼容控制字符；原始字节副本；不支持字符集及不可表示的 ENUM 字典；CHAR 长度/填充和元数据长度不整除。

公开 ReadAuto 损坏测试修改真实页内 ASCII、页外 utf8mb3，并把零宽 CHAR 的长度改为1。修改后重算 CRC，保证继续验证字段逻辑，而不是只触发校验和失败。所有错误都要求返回 nil 结果，不泄露前面已读的部分行。

旧测试中“CHAR/BINARY/VARCHAR/VARBINARY 长度零必须拒绝”的断言已随本阶段契约更新；负长度和其他非法属性仍拒绝。旧 utf8mb4 数据、默认 schema、字符串结果保持兼容；新增 TextBytes 对所有受支持 CHAR/VARCHAR/TEXT 生效。旧阶段文档中的未支持说明是当时的历史边界。

<a id="5-复现与本阶段边界"></a>

## 复现与本阶段边界

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzCharset$' -fuzztime=10s

gzip -dc testdata/charset/charset_latin1_bin.ibd.gz > /tmp/charset_latin1.ibd
go run ./examples/auto /tmp/charset_latin1.ibd
```

默认测试无需启动 MySQL。21 个文件均通过官方严格 crc32 校验，42 个 SDI 对象与 ibd2sdi 和 sdi 示例一致，auto 示例1064行与SQL对照通过；见 [verification.json](../../testdata/charset/verification.json)。原始官方 SDI 以每表 `.sdi.json.gz` 保存，并纳入默认回归。

本阶段只新增已验收的四种编码和九个 collation ID。不支持 gbk/gb18030、UCS2/UTF16 等其他编码；未知 collation 不猜测。二进制 collation 仍是字符列编码，不能混同于 binary 字符集。没有新增字符主键、排序权重算法或事务可见性；沿用新建只插入受控快照、16KiB非压缩非加密DYNAMIC、单整数主键和16MiB单值限制。

本轮完整 race 回归与 vet 通过，核心包覆盖率 93.4%；FuzzCharset 10秒预算完成621192次执行，无失败输入。累计201组资产，其中198组成功资产29785行及3组既有拒绝资产，按快照计数。
