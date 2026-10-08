# 命令行工具使用指南

`innodb-reader` 用于离线读取 InnoDB 稳定快照：查看元数据、导出当前行、按索引查询、验证数据、分析空间分配，以及生成可本地浏览的 HTML 学习报告。本文对应当前七个正式命令和 2026-09-30 首版支持范围，示例于 2026-10-08 使用仓库固定夹具核对。

## 目录

- [构建与快速开始](#构建与快速开始)
- [输入要求与支持范围](#输入要求与支持范围)
- [命令与通用参数](#命令与通用参数)
- [metadata：查看元数据](#metadata查看元数据)
- [export：导出当前行](#export导出当前行)
- [query：按索引查询](#query按索引查询)
- [check：验证指定范围](#check验证指定范围)
- [space 和 page：空间与页信息](#space-和-page空间与页信息)
- [visualize：离线可视化](#visualize离线可视化)
- [分区表文件集合](#分区表文件集合)
- [导出格式与程序读回](#导出格式与程序读回)
- [资源预算](#资源预算)
- [输出、退出码与故障处理](#输出退出码与故障处理)
- [进一步阅读](#进一步阅读)

## 构建与快速开始

需要 Go 1.25 或更高版本，无第三方 Go 依赖。以下命令都在仓库根目录执行；解压示例还需要 `gzip`，分区示例需要 Python 3。示例把输出写入新临时目录，保留仓库原始夹具。

```sh
go build -o /tmp/innodb-reader ./cmd/innodb-reader
CLI_DEMO=$(mktemp -d /tmp/innodb-cli-demo.XXXXXX)
DEMO_TABLE=testdata/mysql8045/lesson_rows.ibd

/tmp/innodb-reader --help
/tmp/innodb-reader export --help
/tmp/innodb-reader metadata "$DEMO_TABLE"
/tmp/innodb-reader check --scope rows "$DEMO_TABLE"
/tmp/innodb-reader export --output "$CLI_DEMO/lesson.jsonl" "$DEMO_TABLE"
cat "$CLI_DEMO/lesson.jsonl"
```

本例表的列为 `id INT`、可空的 `score INT`、可空的 `name VARCHAR(32)`，按聚簇键顺序导出的值为：

| id | score | name |
|---:|---:|---|
| -3 | 42 | `InnoDB` |
| 2 | SQL NULL | SQL NULL |
| 7 | 0 | 空字符串 |
| 12 | -5 | `你好` |

实际文件包含列定义、带类型的行以及完成标记，不是上表的纯文本版本。后续示例沿用 `CLI_DEMO` 和 `DEMO_TABLE`；换用自己的文件时，将输入路径替换为受支持的稳定快照。

## 输入要求与支持范围

- MySQL **8.0.45**，**16 KiB** 页，独立、非压缩、非加密表空间，行格式为 **DYNAMIC 或 COMPACT**。
- 输入必须是普通文件，内容为解压后的物理快照。工具不直接读取 `.ibd.gz`，不从 stdin 接收物理文件，也不负责在线锁表或采集。目标写事务应已结束，文件在整个执行期间保持稳定。
- 默认严格校验实际访问路径的 CRC32C、头尾 LSN 和结构；空间分析依据分配状态区分使用中、空闲、未初始化和文件尾部页。没有关闭校验或损坏抢救模式。
- 行入口从受支持 SDI 自动发现列和索引，不需要外部 schema 文件；正式 CLI 没有 `--schema` 参数。可信显式 schema 和原始 SDI 提取由 Go API/独立示例提供。

当前列值支持范围如下，组合和索引限制以[首版支持矩阵](format/83-release-support-matrix.md)为准。

| 类别 | 已实现能力 |
|---|---|
| 数值 | 五种整数及 UNSIGNED、精确 DECIMAL、有限 FLOAT/DOUBLE、BIT(1..64) |
| 时间 | DATE、YEAR、DATETIME/TIME/TIMESTAMP 的 fsp=0..6；TIMESTAMP 固定输出 UTC |
| 文本与二进制 | CHAR、VARCHAR、BINARY、VARBINARY、四种 TEXT 和四种 BLOB、ENUM、SET、NULL；受支持的当前页外 LOB |
| 文本编码 | utf8mb4、utf8mb3、ascii、MySQL latin1；自动映射限已验证排序规则 |
| 结构化值 | MySQL 二进制 JSON 类型树；二维 GEOMETRY 家族的 SRID/WKB |
| 行与键布局 | 多层聚簇树、复合键、ASC/DESC、唯一非空聚簇键、隐藏 DB_ROW_ID、已验收 UPDATE/DELETE、原生 INSTANT ADD/DROP、STORED/INVISIBLE 物化列 |
| 二级索引 | 受支持的普通 BTREE、唯一/非唯一、NULL、混合方向、字符/二进制前缀、覆盖投影与按需回表 |

完整类型物化的单值上限为 **16 MiB**；`LONGTEXT/LONGBLOB` 不表示可以导出完整 4 GiB 值。VIRTUAL 不执行表达式，必须显式选择仅物化视图。字符索引键仅支持 `utf8mb4_bin / utf8mb3_bin / ascii_bin / latin1_bin`。其他 MySQL 版本、页大小、系统/通用表空间、压缩、加密、FULLTEXT/SPATIAL/函数/多值索引、redo/undo 回放和 MVCC 历史恢复不在当前范围。

## 命令与通用参数

单文件语法：

```text
innodb-reader <命令> [参数] file.ibd
innodb-reader <命令> --help
```

**所有参数必须放在输入文件路径之前**。路径以 `-` 开头时，使用 `--` 结束参数解析；包含空格的路径加引号。

| 命令 | 用途 | stdout 内容 |
|---|---|---|
| `metadata` | 发现表、列、索引入口及不支持原因 | 元数据 JSON |
| `export` | 扫描聚簇树并导出当前未标记删除的行 | 无损 JSONL/CSV |
| `query` | 聚簇或指定二级索引的点查/范围/前导列等值查询 | 无损 JSONL/CSV |
| `check` | 完整检查指定聚簇树、二级树或空间范围 | 对应检查报告 JSON |
| `space` | 严格分析分配结构、段、区、页和索引统计 | 空间报告 JSON |
| `page` | 完整空间分析后选出一个页的信息 | 单页报告 JSON |
| `visualize` | 生成空间、索引树及选定页字节的学习报告 | 自包含 HTML |

所有命令共有：

| 参数 | 默认 | 含义 |
|---|---|---|
| `--output PATH` | stdout | 完整生成后原子发布单个文件；父目录须已存在 |
| `--overwrite` | 关闭 | 允许替换已有输出，必须同时指定 `--output`；输入身份仍受保护 |
| `--help` / `-h` | — | 显示该命令的当前参数，帮助写入 stderr，正常退出 0 |

`--output -` 表示名为 `-` 的文件；要输出到 stdout，省略 `--output`。`metadata / export / check` 另支持 `--manifest PATH`，见[分区表文件集合](#分区表文件集合)。

## metadata：查看元数据

```sh
/tmp/innodb-reader metadata --output "$CLI_DEMO/metadata.json" "$DEMO_TABLE"
python3 -m json.tool "$CLI_DEMO/metadata.json"
```

输出包含数据库/表身份、列定义、`Indexes`、实际 `RootPage`/`ID`/`SpaceID`/`RootVerified`，以及 `Issues`、可用的 `Schema` 和 `MaterializedSchema`。本例聚簇索引为 `PRIMARY`，根页 4，表空间 ID 40，索引 ID 249。

`RootVerified` 表示入口页已核对；`metadata` 退出 0 表示报告已生成，**不保证所有列和整棵树都可读取**。先检查 `Issues`，再决定使用完整行入口或显式仅物化入口。不得从文件名或索引顺序猜测根页号。

专用参数只有 `--manifest` 和元数据页请求预算 `--max-page-reads`；预算默认 1000000。原始 SDI JSON 提取示例见[第41章](format/41-sdi-records.md)，正式 `metadata` 输出的是发现报告。

## export：导出当前行

```sh
# JSONL，附记录物理来源
/tmp/innodb-reader export --format jsonl --physical \
  --output "$CLI_DEMO/lesson-physical.jsonl" "$DEMO_TABLE"

# CSV，沿用相同的带类型值协议
/tmp/innodb-reader export --format csv \
  --output "$CLI_DEMO/lesson.csv" "$DEMO_TABLE"
```

| 专用参数 | 默认 | 含义 |
|---|---|---|
| `--format jsonl\|csv` | `jsonl` | 带类型无损导出格式 |
| `--physical` | 关闭 | 保存记录来源、页号/偏移、事务字段、行版本、原始文本字节、ENUM 序号/SET 掩码等已有元信息 |
| `--materialized` | 关闭 | 显式读取普通/STORED/INVISIBLE 物化列，并报告未求值 VIRTUAL 列 |
| `--manifest PATH` | 无 | 读取完整分区集合，替代单个输入文件 |

扫描预算参数见[资源预算](#资源预算)。单表输出按实际聚簇键顺序排列，值按导出 `columns` 顺序排列；隐藏 DB_ROW_ID 保留在来源中，不变成用户列。delete-mark 和 free 残留不作为导出行；删除证据可通过库接口/学习报告查看，不会恢复成历史 SQL 行。

含 VIRTUAL 的真实样本可这样读取：

```sh
gzip -dc testdata/generated/mixed_instant.ibd.gz > "$CLI_DEMO/materialized.ibd"
/tmp/innodb-reader metadata "$CLI_DEMO/materialized.ibd"
/tmp/innodb-reader export --materialized \
  --output "$CLI_DEMO/materialized.jsonl" "$CLI_DEMO/materialized.ibd"
/tmp/innodb-reader check --materialized "$CLI_DEMO/materialized.ibd"
```

省略 `--materialized` 时，完整行入口拒绝该样本。使用此参数后，schema 的 `virtual_columns` 单独说明未物化列，行中没有伪造的 NULL 占位；也不能借此绕过函数索引内部隐藏列等其他不支持布局。

## query：按索引查询

`--query PATH` 必填，读取最多 **1 MiB** 的 UTF-8 JSON 文件：恰好一个对象，不接受未知字段。此命令使用范围对象，不解析 SQL。通用输出、格式、来源、物化和扫描预算参数与 `export` 相同；另有 `--index NAME` 选择二级索引。**聚簇查询省略 `--index`**，二级索引名从 `metadata` 的 `Indexes` 获取。

### 聚簇点查与范围

点查 `id=7`，上下界均闭合：

```sh
cat > "$CLI_DEMO/point.json" <<'JSON'
{"Lower":{"Key":[7],"Inclusive":true},"Upper":{"Key":[7],"Inclusive":true}}
JSON
/tmp/innodb-reader query --query "$CLI_DEMO/point.json" \
  --output "$CLI_DEMO/point.jsonl" "$DEMO_TABLE"
```

结果为一行 `[7,0,""]`，仍使用带类型封套。未命中或键仅有删除标记时，正常成功并输出零行。

查询 `-3 < id <= 12`，逆序，成功取最多两行：

```sh
cat > "$CLI_DEMO/range.json" <<'JSON'
{"Lower":{"Key":[-3],"Inclusive":false},"Upper":{"Key":[12],"Inclusive":true},"Reverse":true,"Limit":2}
JSON
/tmp/innodb-reader query --query "$CLI_DEMO/range.json" \
  --output "$CLI_DEMO/range.jsonl" "$DEMO_TABLE"
```

本例依次返回 `id=12、7`。范围对象字段如下：

| 字段 | 含义 |
|---|---|
| `Lower` / `Upper` | 完整键边界对象；省略或 `null` 表示无界 |
| `Key` | 按索引成员顺序的数组，边界必须含全部查询键成员 |
| `Inclusive` | `true` 为闭边界，省略/`false` 为开边界 |
| `Prefix` | 非空数组，匹配完整前导列等值；与 `Lower/Upper` 互斥 |
| `Reverse` | 反转访问和输出方向，默认 `false` |
| `Limit` | 成功交付的行数上限，省略/0 不限制；不等于失败预算 `--max-rows` |

上下界按索引声明的每成员 **ASC/DESC 顺序** 比较，`Reverse` 不交换上下界。倒置区间或相等的开放区间正常返回零行。`Complete` 只认证本次范围/Limit 和访问路径，不能据此认定未访问子树也通过检查；`LimitReached=true` 不保证还有更多行。

### 复合键与前导列等值

以下样本的键是 `(tenant ASC, code DESC, seq ASC)`。只限定 `tenant=1`，取前三行：

```sh
gzip -dc testdata/query/mixed_rows.ibd.gz > "$CLI_DEMO/mixed-key.ibd"
cat > "$CLI_DEMO/prefix.json" <<'JSON'
{"Prefix":[1],"Limit":3}
JSON
/tmp/innodb-reader query --query "$CLI_DEMO/prefix.json" \
  --output "$CLI_DEMO/prefix.jsonl" "$CLI_DEMO/mixed-key.ibd"
```

`Prefix:[1,"abc"]` 表示前两列完整值等于 `1` 和 `"abc"`，不是 `code LIKE 'abc%'`。完整边界则须提供三个成员。无显式主键且使用隐藏 DB_ROW_ID 的表，键数组含一个 48 位无符号物理身份值。

### 二进制与大整数键

二进制键成员使用仅有 `base64` 字段的对象；不能用普通字符串代替字节。下面查询 `(k=00 FF, tie=7)`：

```sh
gzip -dc testdata/keys/keys_varbinary_asc.ibd.gz > "$CLI_DEMO/binary-key.ibd"
cat > "$CLI_DEMO/binary.json" <<'JSON'
{"Lower":{"Key":[{"base64":"AP8="},7],"Inclusive":true},"Upper":{"Key":[{"base64":"AP8="},7],"Inclusive":true}}
JSON
/tmp/innodb-reader query --query "$CLI_DEMO/binary.json" \
  --output "$CLI_DEMO/binary.jsonl" "$CLI_DEMO/binary-key.ibd"
```

整数键直接写精确 JSON 整数，CLI 使用 `UseNumber` 读取。不要先通过 JavaScript `Number` 或其他浮点类型生成大整数：

```sh
gzip -dc testdata/query/unsigned_rows.ibd.gz > "$CLI_DEMO/unsigned-key.ibd"
cat > "$CLI_DEMO/unsigned.json" <<'JSON'
{"Lower":{"Key":[18446744073709551615],"Inclusive":true},"Upper":{"Key":[18446744073709551615],"Inclusive":true}}
JSON
/tmp/innodb-reader query --query "$CLI_DEMO/unsigned.json" \
  --output "$CLI_DEMO/unsigned.jsonl" "$CLI_DEMO/unsigned-key.ibd"
```

字符键为 UTF-8 字符串；DECIMAL 和时间键使用与列精度一致的规范字符串。工具严格验证类型、范围、字符集和精度，不模拟 SQL 隐式类型转换。

### 二级查询、覆盖投影与回表

二级查询文件使用 `{"Range":{...},"Columns":[...]}` 封套。`Range` 只描述二级索引**用户声明成员**，不用补聚簇定位后缀；`Columns` 按指定顺序输出。省略或 `null` 表示全部物化列，空数组、重复、未知或 VIRTUAL 列拒绝。聚簇查询没有 `Columns` 投影字段。

下面的样本有二级索引 `s(n)`，查询 `n=NULL` 的前三项并输出 `n,id`：

```sh
gzip -dc testdata/secondary_query/covering.ibd.gz > "$CLI_DEMO/secondary.ibd"
/tmp/innodb-reader metadata "$CLI_DEMO/secondary.ibd"
cat > "$CLI_DEMO/covered.json" <<'JSON'
{"Range":{"Lower":{"Key":[null],"Inclusive":true},"Upper":{"Key":[null],"Inclusive":true},"Limit":3},"Columns":["n","id"]}
JSON
/tmp/innodb-reader query --index s --query "$CLI_DEMO/covered.json" --physical \
  --output "$CLI_DEMO/covered.jsonl" "$CLI_DEMO/secondary.ibd"
```

本例完整值都在索引内，`physical.Covered=true`，没有聚簇页来源。前三个 id 为 `9007199254740992、9007199254741015、9007199254741038`，应按精确整数消费。键成员 `null` 明确表示索引 NULL，和无界端点不同；这采用索引排序语义，不模拟 SQL 三值逻辑。

加入未存于二级索引的 `marker` 后，自动按需回表：

```sh
cat > "$CLI_DEMO/lookup.json" <<'JSON'
{"Range":{"Lower":{"Key":[null],"Inclusive":true},"Upper":{"Key":[null],"Inclusive":true},"Limit":3},"Columns":["n","id","marker"]}
JSON
/tmp/innodb-reader query --index s --query "$CLI_DEMO/lookup.json" --physical \
  --output "$CLI_DEMO/lookup.jsonl" "$CLI_DEMO/secondary.ibd"
```

本例 `Covered=false`，同时保留二级和聚簇位置。只读取谓词/投影需要的 LOB，未选 `payload` 不会为了补全行而读取其完整页外值。回表和二级树共用累计预算；覆盖成功不认证未访问的聚簇树。

前缀索引 `s(name(3), rank_value DESC)` 也按完整原列值解释条件：

```sh
gzip -dc testdata/secondary_query/prefixes.ibd.gz > "$CLI_DEMO/secondary-prefix.ibd"
cat > "$CLI_DEMO/secondary-prefix.json" <<'JSON'
{"Range":{"Prefix":["abd001"],"Limit":2},"Columns":["id"]}
JSON
/tmp/innodb-reader query --index s --query "$CLI_DEMO/secondary-prefix.json" \
  --output "$CLI_DEMO/secondary-prefix.jsonl" "$CLI_DEMO/secondary-prefix.ibd"
```

本例返回 id `161、241`。存储前缀 `abd` 只用于保守导航，工具取得完整 `name` 后复核；相同前缀的其他原值不算命中。输出仍按二级完整物理键序排列，前缀索引顺序不能冒称原列完整值排序。

## check：验证指定范围

```sh
# 默认 rows：完整聚簇树及当前行值
/tmp/innodb-reader check --scope rows "$DEMO_TABLE"

# 空间分配及已支持页结构
/tmp/innodb-reader check --scope space "$DEMO_TABLE"

# 完整选定二级树，不回表
/tmp/innodb-reader check --scope secondary --index s "$CLI_DEMO/secondary.ibd"
```

| `--scope` | 检查内容 | 参数限制 |
|---|---|---|
| `rows`（默认） | 自动发现并扫描完整聚簇树，解码当前值，返回扫描计数 | 接受扫描预算、`--materialized`、`--manifest`；不接受 `--index` 或空间预算 |
| `secondary` | 验证选定二级物理树/字段/定位键和删除标记 | 必须 `--index NAME`，接受扫描预算；不接受 `--materialized`、清单或空间预算 |
| `space` | 与 `space` 相同的严格分析 | 只接受空间预算；不接受 `--index`、物化参数、清单或扫描预算 |

输出是报告，不包含逐行导出值。检查范围不等于全库、事务可见性或其他索引认证；需要多个范围时分别执行命令。

## space 和 page：空间与页信息

```sh
/tmp/innodb-reader space --output "$CLI_DEMO/space.json" "$DEMO_TABLE"
/tmp/innodb-reader page --number 4 --output "$CLI_DEMO/page-4.json" "$DEMO_TABLE"
python3 -m json.tool "$CLI_DEMO/page-4.json"
```

`space` 报告文件页数、FSP/XDES/INODE/IBUF_BITMAP 分配关系、区/段/索引归属和各页状态；区分空间可分配空闲与页内垃圾/连续空闲。未知使用中页保留原始信息，不能宣称其内部布局已解码。

`page --number N` 必填，页号从 **0** 开始。它先分析**完整空间**再选第 N 页，其他使用中页的错误也会导致失败；它输出页信息，不输出十六进制文件片段。想看字节用 `visualize --pages`。

两者使用 `--max-pages` 和 `--space-max-entries`。空间指标不等于 SQL 可见行数或完整字段校验；`LayoutUsedBytes` 是头部推导的布局占用量，LSN 是日志位置，不能视为访问热点。

## visualize：离线可视化

```sh
# 默认仅空间概览
/tmp/innodb-reader visualize --output "$CLI_DEMO/space.html" "$DEMO_TABLE"

# 完整 PRIMARY 树；嵌入页0、4的原字节和相关详情
/tmp/innodb-reader visualize --index PRIMARY --pages 0,4 --manual-dir docs/format \
  --output "$CLI_DEMO/detail.html" "$DEMO_TABLE"

# 普通二级树
/tmp/innodb-reader visualize --index s --pages 5 \
  --output "$CLI_DEMO/secondary.html" "$CLI_DEMO/secondary.ibd"
```

生成后用本地浏览器打开 HTML，无需服务、网络或 CDN；也可直接打开仓库已有的[报告示例](../examples/visual/README.md)。报告可定位页号、父子页、记录边界、外部引用和 LOB 来源，并高亮选定字节区间。

| 专用参数 | 默认 | 含义 |
|---|---|---|
| `--index NAME` | 无 | 精确选择一棵聚簇或二级树，完整扫描该树 |
| `--pages N,M` | 无 | 嵌入这些页的原字节；最多 256 个，不允许重复、负数或空成员，逗号间不加空格 |
| `--manual-dir PATH` | 无 | 本地解析手册目录；相对路径基于执行目录 |
| `--materialized` | 关闭 | 显式仅物化聚簇视图，必须选择聚簇 `--index`；二级索引不接受此选项 |
| `--max-read-calls N` | 1000000 | 累计底层源读取预算，覆盖空间、发现、索引扫描和原始页读取 |
| `--max-details N` | 10000 | 所选页记录详情数预算 |
| `--max-report-bytes N` | 67108864 | 嵌入 JSON 大小预算，不含静态 HTML 框架 |

空间预算默认最多 **100000** 页，其他命令的空间默认为 1000000 页。索引扫描预算只有指定 `--index` 后才可显式设置。`--pages` 不缩小空间分析或选定树的扫描范围；不选索引时仍可查看所选原字节，但没有行解码详情。

任一请求层失败都不会发布成功文件。报告不嵌入全部用户值或完整页外 LOB 载荷；未嵌入字节的页可以查看统计，字节钻取需重新生成并加入 `--pages`。它也不猜测公开 API 未提供的普通列逐字段偏移；完整值导出使用 `export`。

## 分区表文件集合

普通 RANGE/LIST/HASH/KEY 分区表必须提供**完整**文件集合。`--manifest` 只用于 `metadata / export / check --scope rows`，与单文件位置参数互斥；没有集合查询、全局键归并、子分区或集合 HTML 报告。

下面将固定 RANGE 样本解压到示例目录并生成清单，故意使用 `p2/p0/p1` 输入次序：

```sh
mkdir "$CLI_DEMO/partitions"
python3 - "$CLI_DEMO/partitions" <<'PY'
import gzip, json, sys
from pathlib import Path
out = Path(sys.argv[1])
files = []
for name in ('p2', 'p0', 'p1'):
    source = Path('testdata/partitions/ranges-' + name + '.partition.gz')
    (out / (name + '.ibd')).write_bytes(gzip.decompress(source.read_bytes()))
    files.append({'partition': name, 'path': name + '.ibd'})
(out / 'files.json').write_text(json.dumps({'version': 1, 'files': files}), encoding='utf-8')
PY
/tmp/innodb-reader metadata --manifest "$CLI_DEMO/partitions/files.json"
/tmp/innodb-reader check --scope rows --manifest "$CLI_DEMO/partitions/files.json"
/tmp/innodb-reader export --manifest "$CLI_DEMO/partitions/files.json" \
  --output "$CLI_DEMO/partitions/rows.jsonl"
/tmp/innodb-reader export --manifest "$CLI_DEMO/partitions/files.json" --format csv --physical \
  --output "$CLI_DEMO/partitions/rows.csv"
```

实际清单结构为：

```json
{"version":1,"files":[
  {"partition":"p2","path":"p2.ibd"},
  {"partition":"p0","path":"p0.ibd"},
  {"partition":"p1","path":"p1.ibd"}
]}
```

清单必须是最多 **1 MiB** 的单个 UTF-8 JSON 对象，版本 1，含 1..1024 个文件；分区名对应 SDI 定义，相对路径以**清单目录**为准。固定夹具的 `testdata/partitions/manifest.json` 是测试资产记录，不能直接当作上述 CLI 清单。

预检全部文件成功后，按 SDI **分区定义序**输出，各分区内保持聚簇键序；本例为 `p0/p1/p2`，3 个完成分区、1000 行，整体不保证全局键有序。空分区也不能省略。缺失、重复、额外、跨表或可检测身份矛盾拒绝；元数据一致仍不能证明文件来自同一事务时点，调用者负责稳定采集窗口。

集合导出的 schema 始终 `physical=true`，每条行的来源必含 `partition`；加 `--physical` 后另含 `record`。Values 不增加“分区列”。扫描预算在所有文件间累计，后段失败可能已向 stdout 输出前缀；使用 `--output` 可避免发布不完整文件。

## 导出格式与程序读回

两种格式均为 **rowio 版本 1 带类型协议**，保存列定义和精确值。JSONL 的实际 `lesson_rows` 片段如下（省略其他行；完整文件最后计数为 4）：

```json
{"kind":"schema","schema":{"version":1,"columns":[{"name":"id","type":"INT","nullable":false,"max_chars":0},{"name":"score","type":"INT","nullable":true,"max_chars":0},{"name":"name","type":"VARCHAR","nullable":true,"max_chars":32}],"physical":false}}
{"kind":"row","values":[{"type":"int32","value":"7"},{"type":"int32","value":"0"},{"type":"string","value":""}]}
{"kind":"end","rows":"4"}
```

结构是 **schema → 零或多条 row → end → EOF**。只有核对 `end`、累计行数和尾随 EOF 才能认证完整；上面的删节展示不能当作完整协议文件。

| 值 | 单元格示意 | 含义 |
|---|---|---|
| SQL NULL | `{"type":"null"}` | 与空文本、空二进制分开 |
| 空文本 | `{"type":"string","value":""}` | 字符串 |
| 二进制 00 FF | `{"type":"binary","value":"AP8="}` | Base64 原始字节；空字节 value 为 `""` |
| 大整数 | `{"type":"uint64","value":"18446744073709551615"}` | 十进制字符串，type 保留 Go 位宽 |
| DECIMAL | `{"type":"string","value":"123.4500"}` | 保留精度和尾零，SQL 类型见 columns |
| 浮点负零 | `{"type":"float32","value":"-0"}` | 保留位宽和零符号 |
| JSON 列 | `type=json` 的递归树 | 保留 kind、binary_type、精确标量和 opaque；JSON null 与 SQL NULL 分离 |
| 空间列 | `type=geometry` 的 srid/wkb | 保留 SRID 和 Base64 WKB，读回重建几何结构 |

CSV **没有普通列名标题行**：首记录只有一个 schema JSON 单元格，随后每列保存上述类型单元格 JSON；有来源时最后再加一个 physical 单元格。CSV 外层负责引号，JSON 内层负责控制字符和换行，因此每个协议记录占一物理行。它不适合依赖表格软件自动推断类型，也不是 SQL dump 或数据库导入文件。

CSV 没有完成尾行，**EOF 不证明生产成功**，必须检查命令退出码，或使用完整成功后原子发布的 `--output` 文件。两种格式都要求最后终止换行，默认每条协议记录最多 128 MiB。

程序读回使用公开 [`rowio.NewDecoder`（rowio.go:241）](../rowio/rowio.go#L241)：`NewDecoder(reader, "jsonl" 或 "csv", Options{})`，`Header()` 获取列定义，循环 `Next()` 恢复原 Go 值。JSONL 正常 EOF 后还应检查 `Complete()`；CSV 的 `Complete()` 始终为 false。完整代码示例见[第77章](format/77-cli-and-lossless-export.md)。

单表 `--physical` 保存 Record/ProjectedRow 去掉 Values 后的来源；只有选择来源时才保留 ENUM 原序号、SET 掩码、CHAR 物理空格等额外信息。physical 和普通 metadata/space/check JSON 的大整数仍是 JSON 数字，读回无类型对象时须 `UseNumber` 或等效精确整数方式。可视化报告单独将数字编码为精确十进制字符串。

## 资源预算

这些是处理计量上限，**不是进程 RSS 上限**。超限报错并退出 1，不自动截断成成功结果；完整值 16 MiB、SDI、JSON/空间类型等固定限制不会随行预算放大。

| 参数 | 默认值 | 适用范围与计量 |
|---|---:|---|
| `--cache-pages` | 64 | export/query/check rows或secondary/visualize索引扫描；-1 禁用，0 使用库默认，低于 -1 拒绝 |
| `--max-page-reads` | 1000000 | metadata 页请求；扫描/查询页请求含缓存命中，export/query 还扣除预检读取 |
| `--max-entries` | 1000000 | 扫描/查询累计遍历项，含必要导航/记录/LOB状态 |
| `--max-rows` | 1000000 | 扫描/查询交付当前行数预算；secondary check 计二级项 |
| `--max-row-bytes` | 67108864（64 MiB） | 单行/候选所用源字节预算，不等于已编码行大小或 Go 堆大小 |
| `--max-pages` | 1000000；visualize 为100000 | space/page/check space/visualize 的输入文件页数 |
| `--space-max-entries` | 10000000 | 上述空间分析的结构遍历预算 |
| `--max-read-calls` | 1000000 | visualize 底层累计源读取 |
| `--max-details` | 10000 | visualize 所选记录详情 |
| `--max-report-bytes` | 67108864（64 MiB） | visualize 嵌入报告 JSON 字节 |

数值 0 选择对应库入口的默认值，不表示无限制；例如 `visualize --max-pages 0` 仍为 100000 页。`metadata --manifest` 会用集合预检的默认扫描预算，公开参数仍只有元数据命令帮助列出的项目。

例如显式提高可能因多次回表而累计的遍历预算：

```sh
/tmp/innodb-reader query --index s --query "$CLI_DEMO/lookup.json" \
  --max-entries 10000000 --cache-pages 128 \
  --output "$CLI_DEMO/lookup-larger-budget.jsonl" "$CLI_DEMO/secondary.ibd"
```

export/query 的 stderr `preflight_reads` 单独报告预检源读取，API `report.PageReads` 报告之后的请求；统计总请求时两者相加。SDI 对象/解压容量限制独立，二级回表和分区读取不重置累计预算。

## 输出、退出码与故障处理

stdout 只输出请求数据；帮助、诊断和最终执行摘要走 stderr。摘要包含 `command / scope / complete / error`，以及适用的 `preflight_reads / report`。库报告的 `Complete` 与外层 `complete` 应分别按请求范围和最终执行结果解释。

| 退出码 | 含义 |
|---:|---|
| 0 | 请求及输出成功；正常显示帮助也为 0 |
| 2 | 命令/参数、查询文件或清单格式错误 |
| 1 | 不支持、损坏、预算、输入/输出 I/O、编码或发布失败；与实际 schema 不符的查询键也在运行时失败 |
| 130 | 取消，包括 SIGINT/SIGTERM |

导出并保留执行状态：

```sh
/tmp/innodb-reader export --output "$CLI_DEMO/checked.jsonl" "$DEMO_TABLE" \
  2> "$CLI_DEMO/export-summary.json"
cli_exit=$?
printf 'exit=%s\n' "$cli_exit"
cat "$CLI_DEMO/export-summary.json"
```

正常本例为 `complete=true`、4 行。文件输出同目录临时写入，完成编码、同步和关闭后发布；默认已有目标会报错。只有明确指定 `--overwrite --output PATH` 才替换，失败保留原目标；输入文件、查询文件、分区清单及全部输入的可识别别名均受保护。保证单文件原子可见性，不承诺目录断电持久性。发布后 stderr 写失败仍会非零退出，已发布文件无法撤回。

对比“预算失败”和“成功取 N 行”：

```sh
# 本例有4行；预算1使导出失败，原目标保持原内容
printf 'original\n' > "$CLI_DEMO/kept.jsonl"
/tmp/innodb-reader export --max-rows 1 --overwrite \
  --output "$CLI_DEMO/kept.jsonl" "$DEMO_TABLE"
cli_exit=$?
printf 'exit=%s\n' "$cli_exit"
cat "$CLI_DEMO/kept.jsonl"

# 成功取1行，应使用查询的 Limit
printf '%s\n' '{"Limit":1}' > "$CLI_DEMO/one.json"
/tmp/innodb-reader query --query "$CLI_DEMO/one.json" \
  --output "$CLI_DEMO/one.jsonl" "$DEMO_TABLE"
```

第一条导出退出 1，文件仍为 `original`；第二条查询退出 0，完成标记为 `rows="1"`。直接重定向 stdout 不享受原子发布：shell 会先打开/截断目标，失败可能留下前缀。JSONL 失败前缀没有成功 end；CSV 即便在完整行边界结束，也必须核对生产者退出码。管道提前关闭按写失败处理；使用管道时还须取得生产者状态，不能只看最后一个消费者的退出码。

| 现象 | 处理 |
|---|---|
| 参数放在输入路径后面 | 将所有选项移到路径前，查对应 `--help` |
| metadata 成功，export 失败 | 看 Issues/Schema；确实只需要物化列时加 `--materialized` |
| 查询键宽度、字符集或精度错误 | 按实际索引成员和列定义修改 JSON；不用 SQL 隐式转换规则猜测 |
| 预算耗尽 | 看报错的计量项和摘要，只调整对应预算；`Limit` 用于正常限行 |
| CRC/LSN、链或身份矛盾 | 核查稳定快照来源和文件完整性，保留错误证据；工具没有跳过校验开关 |
| 超过16 MiB完整值 | 接受类型物化上限；若仅需原始大LOB字节，使用库 `StreamLOB`，正式CLI没有原始LOB流命令 |
| 单个分区无法读全表 | 提供完整清单，包含空分区；检查名称、路径和物理身份 |
| 空间/局部查询成功，全表检查失败 | 两者验证范围不同；按需要执行 rows、secondary 或 space 检查 |
| 目标已存在 | 使用新的输出路径，或显式 `--overwrite`；不能覆盖输入 |
| HTML页详情没有字节 | 重新生成并用 `--pages` 加入需要的页，索引详情还需要 `--index` |

取消为协作式检查，不能保证强制打断任意阻塞底层 I/O。任何失败时都不能仅凭“已看到几行”宣称请求完整成功。

## 进一步阅读

- [首版支持矩阵](format/83-release-support-matrix.md)：当前支持、明确拒绝与未验证范围。
- [构建与复验](format/84-release-validation.md)：完整回归、性能记录与故障复现。
- [CLI与无损协议](format/77-cli-and-lossless-export.md)及[原子发布与失败](format/78-cli-validation-and-failure.md)：协议细节和 Go 读回示例。
- [聚簇查询](format/69-key-query-navigation.md)、[二级查询](format/73-secondary-query-navigation.md)及[投影与回表](format/74-projection-and-lookup-validation.md)。
- [分区集合](format/79-partition-collection-layout.md)及[分区CLI验证](format/80-partition-validation-and-cli.md)。
- [空间分析](format/75-space-allocation-layout.md)、[可视化模型](format/81-visual-report-model.md)及[报告验证](format/82-visual-report-validation.md)。

实现核对入口：[命令与参数（run.go:40）](../internal/cli/run.go#L40)、[查询文件（query.go:14）](../internal/cli/query.go#L14)、[分区清单（partition.go:27）](../internal/cli/partition.go#L27)、[文件发布（output.go:41）](../internal/cli/output.go#L41)、[信号与退出码（main.go:11）](../cmd/innodb-reader/main.go#L11)。
