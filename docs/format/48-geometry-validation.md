# 48. 空间值的页外读取、验证与边界

<a id="48空间值的页外读取验证与边界"></a>

本章接续[第 47 章](47-geometry-storage.md)，解释为什么“成功打印坐标”还不足以证明空间列读取正确，以及怎样逐层验证。

<a id="1-真实夹具与独立预期"></a>

## 真实夹具与独立预期

[manifest](../../testdata/geometry/manifest.json) 保存 12 个不可变快照的哈希、行数、space/index/root 和实例配置。MySQL 版本为 8.0.45，16 KiB 页、crc32、file-per-table、DYNAMIC。新建专用库 `innodb_reader_geometry_c7eae7464cb2`；原实例继续运行，没有修改既有表或全局配置。

| 夹具 | 行数 | 验证目的 |
|---|---:|---|
| geometry_lesson | 10 | 一般 GEOMETRY 中的七种类型、NULL、空集合、嵌套、4326 |
| geometry_point 至 geometry_geometrycollection | 各 2，共 14 | 七种具体列类型，每组 NULL 与非 NULL |
| geometry_srid | 2 | 声明 SRID=4326、南西与东北坐标、SQL 轴序差异 |
| geometry_zero | 1 | 声明 SRID=0，与无声明区分 |
| geometry_large | 1 | 6000 点 LineString，96013 字节，真实页外值 |
| geometry_tree | 600 | 七种类型混合，七个聚簇索引页 |

共 628 行、一处页外字段、三行 4326 坐标。各表几何列后有 `after-geometry`，用以检查字段边界。

生成器 [generate_geometry_fixtures.py](../../scripts/generate_geometry_fixtures.py) 只使用 Python 标准库。每个新表在同一连接执行 `FLUSH TABLES ... FOR EXPORT`，保持锁期间查询 SQL 预期、读取文件，然后 `UNLOCK TABLES`。它不是直接读取正在变化的文件。生成 SQL 保存于 generate.sql.gz，密码仅来自 MYSQL_PWD，未落入资产。

SQL 预期独立保存：

- `HEX(doc)`：包含 SRID 的完整存储值。
- `HEX(ST_AsBinary(doc,'axis-order=long-lat'))`：WKB 原始字节对照。
- 默认 `ST_AsBinary`：4326 的轴序差异证据。
- `ST_SRID`、`ST_GeometryType`：类型及 SRID。
- `ST_AsGeoJSON(doc,17,0)`：本批可精确表示坐标的层次对照。
- id、SQL NULL 标志、note：行身份及相邻字段边界。

MySQL `ST_GeometryType` 用 `GEOMCOLLECTION` 拼写，本 API 使用完整 `GEOMETRYCOLLECTION`；测试仅将这个已知别名归一化。SQL GeoJSON 仅用作独立验证，不是新增的公共输出 API。通用任意浮点数据仍以 WKB 位模式为精确依据。

<a id="2-从-20-字节引用恢复大-linestring"></a>

## 从 20 字节引用恢复大 LineString

`geometry_large` 的 id=1 位于页 4 origin=129。doc 从页内 146 开始，局部只有如下 20 字节：

```text
00 00 00 d2  00 00 00 05  00 00 00 01  00 00 00 00 00 01 77 0d
space=210    first_page=5  version=1    length=96013
```

引用字段按既有 LOB 格式读取；这里的大端引用和几何载荷的小端不是一回事。origin 前的变长元信息含 `14 c0`，逆向解码表示 external、局部长度 20；`0e` 是后续 note 的长度。

活动块从 `Record.External[0].Chunks` 得到：

| LOB 页 | 页内数据偏移 | 长度 | 首页面内索引项偏移 |
|---:|---:|---:|---:|
| 5 | 696 | 15680 | 96 |
| 6 | 49 | 16327 | 156 |
| 7 | 49 | 16327 | 216 |
| 8 | 49 | 16327 | 276 |
| 9 | 49 | 16327 | 336 |
| 10 | 49 | 15025 | 396 |

总长度恰好 96013。第一块文件偏移 `5×16384+696=82616`，起始字节：

```text
00 00 00 00 01 02 00 00 00 70 17 00 00
SRID=0      LINESTRING     点数=0x1770=6000
```

完整长度 `4+5+4+6000×16=96013`，后续坐标为 `(i,i%13)`，i=0..5999。必须拼接完整逻辑字段后解码；不能假设一个点恰好位于一个 LOB 块中，更不能将第一页剩余内容当成整个值。

`Record.End=180` 仍只描述聚簇页内记录；`Result.Pages` 对这个表仍只有根叶子页。LOB 的六个块通过 External 单独报告，不混入聚簇树访问顺序。

<a id="3-解析防护与错误约定"></a>

## 解析防护与错误约定

解码前检查 16 MiB 单值上限。所有计数先与剩余字节及资源预算比较，再分配内存；坐标数使用足够宽的整数比较，避免恶意 uint32 计数溢出或巨量分配。

| 限制/结构 | 当前契约 |
|---|---|
| 单字段字节数，包含 SRID | 最多 16 MiB |
| 几何节点总数，包含根 | 最多 100000 |
| 坐标对总数，包含闭环末点 | 最多 1000000 |
| 节点深度，根为 1 | 最多 100 |
| 空几何 | 允许 GeometryCollection；不支持空 Point 方言 |
| 坐标 | 有限 binary64；保留负零，不做 SRS 范围推断 |
| WKB 扩展 | Z/M/EWKB 等返回 ErrUnsupported |
| 错误输出 | DecodeGeometry 返回零值；Read/ReadAuto 返回 nil 结果 |

截断、非法字节序、越界计数、错误 Multi 子类型、未闭合环、非有限坐标、尾随垃圾以及声明不匹配返回 ErrCorrupt。资源保护和未实现格式返回 ErrUnsupported。该上限是本实现的预算，不是 MySQL 类型本身的容量。

[geometry_test.go](../../geometry_test.go) 分别验证：真实快照哈希及完整 SQL 对照；自动/手工 schema 和读取结果完全相同；官方 SDI；大小端及混合子节点、负零/次正规数、WKB 独立副本；每个截断位置；非法结构和各项预算临界值。合成最大深度/节点/坐标测试不冒称为 MySQL 实际生成的超大几何。

公开入口损坏测试分别修改页内 POINT 与页外 LineString 的 WKB 字节序。修改后重新计算页 CRC，因此能确认错误来自字段解析，而非只被页校验挡住。所有错误都检查没有部分返回。

<a id="4-复现与验收证据"></a>

## 复现与验收证据

离线执行，不需要运行 MySQL：

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzGeometry$' -fuzztime=10s
```

查看结构化结果：

```sh
gzip -dc testdata/geometry/geometry_srid.ibd.gz > /tmp/geometry_srid.ibd
go run ./examples/auto /tmp/geometry_srid.ibd
```

默认 JSON 输出包含 SRID、Base64 编码的 WKB、Geometry.Type/ByteOrder 与 points/rings/geometries；SQL NULL 仍为 null。不要把 Base64 当作 WKT 文本。手工 schema 示例见 [geometry_srid.json](../../testdata/geometry/geometry_srid.json)。

12 个文件均通过 MySQL 8.0.45 `innochecksum --strict-check=crc32`。24 个 SDI 对象与官方 ibd2sdi、项目 sdi 示例逐对象一致；auto 示例的 628 行 SRID/WKB/NULL/相邻列与 SQL 一致，工具版本和计数见 [verification.json](../../testdata/geometry/verification.json)。官方输出保存为各表 `.sdi.json.gz`，默认测试持续对照，验证无需再次连接实例。

第二十二阶段加入后累计 180 组真实资产：177 组成功资产共 28721 行，另有 1 组超限和 2 组二级索引拒绝资产，按快照计数。本阶段不扩展空间索引、坐标转换、几何运算、拓扑有效性判定、旧版固定 POINT、事务可见性或恢复范围。

本轮完整 race 回归和 vet 通过，核心包覆盖率 92.9%；FuzzGeometry 10 秒预算完成 2584478 次执行，没有失败输入。
