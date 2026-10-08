# 47. 空间列的 SRID、WKB 与坐标树

<a id="47空间列的-sridwkb-与坐标树"></a>

本章从真实 POINT 记录出发，解释怎样把一个空间字段完整还原成 SRID、坐标和几何层次。前置知识是[变长字段](11-variable-lengths.md)、[页外 LOB](13-lob-reference-and-pages.md)及[自动 schema](43-sdi-schema.md)。验收过程见[第 48 章](48-geometry-validation.md)。

<a id="1-一列几何值的三层结构"></a>

## 一列几何值的三层结构

空间值是聚簇记录中的一个普通字段，不等于空间索引。当前表只有整数主键；读取空间值不需要构建 R-tree。

```text
聚簇记录的 NULL 位与变长长度
  ├─ NULL：没有字段载荷，Values 返回 nil
  ├─ 页内：完整字段字节
  └─ 页外：20 字节引用 → 按活动 LOB 链拼接完整字段
                              ↓
                   4 字节 SRID + WKB
                              ↓
                GeometryValue{SRID, WKB, Geometry}
```

SRID 标识空间参考系统；它不是坐标数、字节长度或几何类型。WKB（Well-Known Binary）描述几何类型和坐标。本实现没有 SRS 字典，不自动解释坐标单位、交换轴或转换坐标系。

字段内部偏移均相对于**完整字段起点**：

| 偏移 | 字节数 | 含义 | 字节序 |
|---|---:|---|---|
| 0 | 4 | SRID | 始终小端 |
| 4 | 1 | 顶层 WKB 字节序标记 | 0=大端，1=小端 |
| 5 | 4 | 顶层几何类型 | 由偏移 4 的标记决定 |
| 9 | 可变 | 类型主体 | 由本节点标记决定 |

注意：SRID 没有包含在 `GeometryValue.WKB` 中。要恢复整个字段，用小端写入 SRID，再拼接 WKB。返回的 WKB 是独立副本，可以核对每一位，而不是将坐标重新编码得到的近似证据。

<a id="2-逐字节还原真实-point"></a>

## 逐字节还原真实 POINT

使用 [geometry_point.ibd.gz](../../testdata/geometry/geometry_point.ibd.gz)，SQL 定义为 `id INT PRIMARY KEY, doc POINT NULL, note VARCHAR(32)`。id=1 是 NULL；id=2 是 `POINT(12.5 -7.25)`。

id=2 位于页 4，记录 origin=166，End=222。这个 schema 的物理数据先放 4 字节 id、6 字节事务 ID 和 7 字节回滚指针，共 17 字节。因此 doc 从页内 **183** 开始，文件绝对偏移为 `4×16384+183=65719`，长度 25。不能把本例的 17 当作任意表的通用常量。

记录 origin 前八字节是：

```text
0e 19 00 00 00 18 ff ca
```

这里逆向读取的变长长度包含 `19`（doc=25 字节）与 `0e`（note=14 字节）；NULL 位图为零。其余五字节属于记录头，沿用第 03 章。POINT 虽然这个值恰好是 25 字节，当前 MySQL 8.0.45 新建表仍将其作为变长字段记录长度，不能跳过长度数组。

doc 的完整 25 字节：

```text
00 00 00 00                 SRID=0
01                          小端 WKB
01 00 00 00                 类型=1（POINT）
00 00 00 00 00 00 29 40     X=12.5
00 00 00 00 00 00 1d c0     Y=-7.25
```

坐标是 IEEE 754 binary64，与普通 DOUBLE 一样可以用 `math.Float64frombits` 恢复。区别在于这里要服从各 WKB 节点的字节序，不应用整数列的符号翻转。解码后得到：

```go
GeometryValue{
    SRID: 0,
    // WKB 保留上面除 SRID 外的 21 字节。
    Geometry: Geometry{
        Type: "POINT", ByteOrder: 1,
        Points: []Coordinate{{X: 12.5, Y: -7.25}},
    },
}
```

页内 doc 为 `[183,208)`，note 为 `[208,222)`，note 的原始 ASCII 是 `after-geometry`。这也独立检验了字段边界，防止解码器读对坐标却吞掉后续列。

<a id="3-七类-wkb-的共同语法"></a>

## 七类 WKB 的共同语法

以下偏移相对于**当前 WKB 节点**，不是整个字段；每个节点均有自己的五字节头。

| 类型号 | 类型 | 五字节头之后 |
|---:|---|---|
| 1 | POINT | X、Y 各 8 字节 |
| 2 | LINESTRING | uint32 点数，再连续读取点数×16 字节 |
| 3 | POLYGON | uint32 环数；每环为 uint32 点数及坐标序列 |
| 4 | MULTIPOINT | uint32 子几何数，再读取完整 POINT 节点 |
| 5 | MULTILINESTRING | uint32 子几何数，再读取完整 LINESTRING 节点 |
| 6 | MULTIPOLYGON | uint32 子几何数，再读取完整 POLYGON 节点 |
| 7 | GEOMETRYCOLLECTION | uint32 子几何数，再读取任意上述类型的完整节点 |

所有计数都用当前节点的字节序。Polygon 的环没有新的类型头和字节序；Multi 的子几何有。把这两种嵌套混淆，会在每个子节点错位五个字节。

真实 `geometry_lesson` 的 id=3，页 4 origin=230，字段起点=247：

```text
00 00 00 00 | 01 02 00 00 00 | 03 00 00 00
SRID=0         LINESTRING       3 个点
```

后面依次为 `(0,0)、(1,2)、(3,4)`，完整长度 `4+5+4+3×16=61`，变长长度字节正是 `3d`。

同一夹具 id=4 的 Polygon，字段从页内 348 开始：

```text
00 00 00 00 | 01 03 00 00 00 | 02 00 00 00 | 05 00 00 00
SRID=0         POLYGON           两个环        第一环五点
```

外环 `(0,0),(8,0),(8,8),(0,8),(0,0)`；内环 `(1,1),(1,2),(2,2),(2,1),(1,1)`。第二环点数位于字段内偏移 97；总长度 `4+5+4+2×(4+5×16)=181`。空间值不是 UTF-8 字符串，不应套用 TEXT 的编码检查。

id=8 是包含 Point 和嵌套 GeometryCollection 的集合。其层次被保存在 `Geometries` 中，而不是展平成点数组。每个子节点的 ByteOrder 也保留；真实存储均是小端，混合大小端由合成测试单独验证。

<a id="4-sql-null-与合法空几何"></a>

## SQL NULL 与合法空几何

`geometry_lesson` 的 id=9 是空 GeometryCollection，页内字段起点=1126：

```text
00 00 00 00 01 07 00 00 00 00 00 00 00
```

它有 SRID、类型和零子节点数，长度 13；返回非 nil 的 GeometryValue，Type 为 `GEOMETRYCOLLECTION`。SQL NULL 则没有这些字节，返回 nil。

本阶段遵循目标版本的结构约束：LineString 至少两点，Polygon 至少一环，每环至少四点且首尾坐标相同；Multi 系列必须非空且子类型匹配。允许空 GeometryCollection，不把 NaN 坐标解释成其他 WKB 方言中的空 Point。

这些检查只保证基本结构，不判断自交、环方向、洞是否位于外环内或拓扑有效性。坐标必须有限；负零及有限浮点位模式通过原始 WKB 保留，结构树也保留负零符号。

<a id="5-srid-4326文件顺序与-sql-输出顺序"></a>

## SRID 4326：文件顺序与 SQL 输出顺序

[geometry_srid](../../testdata/geometry/geometry_srid.sql) 声明 `POINT SRID 4326`。第一行使用：

```sql
ST_GeomFromText('POINT(120 30)',4326,'axis-order=long-lat')
```

页 4 origin=128，字段起点=145，文件偏移=65681，实际字段是：

```text
e6 10 00 00 01 01 00 00 00
00 00 00 00 00 00 5e 40
00 00 00 00 00 00 3e 40
```

`0x10e6=4326`，存储 X=120、Y=30。真实 SQL 对照：

| 输出 | X、Y 顺序 |
|---|---|
| `HEX(doc)` 去掉前四字节 SRID | 120、30 |
| `ST_AsBinary(doc,'axis-order=long-lat')` | 120、30 |
| 默认 `ST_AsBinary(doc)` | 30、120 |
| 本解析器 `Geometry.Points[0]` | 120、30 |

默认 SQL 输出会依据 SRS 的轴定义处理顺序；文件值解码不应再次交换轴。这里的经纬度解释仅针对已核查的 4326 例子，不代表任意 SRID 的 X 都是经度。

<a id="6-自动-schema-与实现入口"></a>

## 自动 schema 与实现入口

实际 SDI：DD type=30、collation_id=63、char_length=4294967295；`options` 中 `geom_type=0..7` 分别代表一般 GEOMETRY 和七种具体类型。不能只凭 DD type=30 猜成 POINT。

`srs_id_null=true` 表示没有 SRID 声明；false 时读取 `srs_id`。Go `Column.SRID *uint32` 对应这个区别：nil 是不限制，指向 0 是明确要求 SRID=0；JSON schema 可写 `"srid":4326`。具体类型列与 SRID 声明均在字段解码后核对，不匹配报损坏，不返回部分行。

代码入口：

- [geometry.go](../../geometry.go)：DecodeGeometry、计数/坐标/递归解码、声明核对。
- [schema.go](../../schema.go)：空间类型的变长容量及可选 SRID。
- [metadata.go](../../metadata.go)：SDI 类型、geom_type、SRID 映射。
- [record.go](../../record.go)：页内与完整 LOB 字节交给统一解码器。

源码依据为用户提供的 MySQL 8.0.45 源码根：`sql/spatial.h:51–55` 定义 SRID/WKB 头大小，`:312–313` 定义字节序，`:720` 说明 MySQL 可移植格式采用小端；`sql/gis/wkb.cc:264–357` 处理点数、环和集合，`:433` 读取小端 SRID，`:514` 写入 SRID；`sql/dd/impl/types/column_impl.cc:355–358` 序列化 SRID 声明。旧 DATA_POINT 路径存在于 InnoDB 源码中，但不能据此声称已支持旧版固定 POINT 布局；本阶段验收的是 8.0.45 新建 DYNAMIC 表。
