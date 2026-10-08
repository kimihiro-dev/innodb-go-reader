# 55：没有 PRIMARY KEY，InnoDB 怎样组织整行

前置知识是第 51–54 章的完整聚簇键、物理字段顺序及树范围。本章解决一个更早的问题：表没有显式 PRIMARY KEY 时，到底哪棵树包含完整用户行？验收和重复行身份见[第 56 章](56-rowid-layout-validation.md)。

## 1. 三种聚簇身份

InnoDB 每张表都有聚簇索引，其叶子保存完整行。聚簇不等于 SQL 中一定存在显式主键。

| 表定义 | 聚簇键 | 用户是否声明了这些键列 |
|---|---|---|
| 有显式 PRIMARY KEY | 该主键的完整成员 | 是 |
| 无显式主键，有合适的 UNIQUE | 实际被选中的完整非空唯一键 | 是，但它仍是 UNIQUE |
| 没有合适的候选键 | 隐藏六字节 DB_ROW_ID | 否 |

“合适”至少要求成员非 NULL、非虚拟、完整而非前缀；MySQL 还排除几何候选，并有 BLOB 完整容量的专门判断。本项目只读取阶段 25 已支持的完整键类型和四个字符排序规则。若真实聚簇键使用了未支持的类型或规则，必须拒绝；不能退而选择另一个更容易解析的唯一索引，因为后者的叶子并不保存完整行。

源码核查位置（相对本地 MySQL 8.0.45 源码根）：

- `storage/innobase/handler/ha_innodb.cc:14820–14860`：选择 PRIMARY 或候选 UNIQUE。
- `sql/dd/impl/types/index_impl.cc:351–389`：`is_candidate_key` 的可空、虚拟、几何和前缀检查。
- `storage/innobase/handler/ha_innodb.cc:14937–15022`：没有候选时添加隐藏列/索引，并为聚簇记录追加事务字段和其余用户列。
- `storage/innobase/dict/dict0dd.cc:3187–3207`：创建运行时 GEN_CLUST_INDEX 或先创建所选用户索引。

## 2. 不按建表文本顺序或名字猜测

真实 `unique_multiple` 的声明顺序是：

```sql
UNIQUE KEY nullable_first(nullable),
UNIQUE KEY z_chosen(b),
UNIQUE KEY a_later(a)
```

nullable 列可以为 NULL，b、a 都为 NOT NULL。最终快照的 SQL `INFORMATION_SCHEMA.INNODB_INDEXES` 为：

| 索引 | ID | 根页 | TYPE | 实际角色 |
|---|---:|---:|---:|---|
| z_chosen | 871 | 4 | 3 | 聚簇且唯一 |
| a_later | 872 | 5 | 2 | 唯一二级 |
| nullable_first | 873 | 6 | 2 | 可空唯一二级 |

运行时 TYPE 是标志组合，其中最低位表示聚簇。**不要把它与 SDI 的 type 枚举混用**：这三个索引的 SDI type 都是 2（UNIQUE）。DD 已把实际聚簇索引排在首位，顺序与建表文本不同；按名字排序选 a_later 也会选错。

解析器检查首个索引是否属于支持的聚簇候选，再核对它的完整元素列表：键列在前、事务列居中、剩余用户列在后。不会重新按名字或支持类型筛选候选。显式 PRIMARY 出现在非首位、聚簇字段缺失或顺序不符，都不能提供可读 Schema。

## 3. 隐藏索引的两个名字

`rowid_lesson` 没有任何用户索引，真实 SDI 中却存在：

```text
列：n, text, DB_ROW_ID, DB_TRX_ID, DB_ROLL_PTR
索引：name=PRIMARY, type=2, hidden=true, is_generated=false
元素：DB_ROW_ID, DB_TRX_ID, DB_ROLL_PTR, n, text
所有元素：hidden=true, order=2, length=4294967295
```

运行时 SQL 字典给这棵树的名字是 `GEN_CLUST_INDEX`。所以看到 SDI 名为 PRIMARY，不能就当作用户显式主键；看到 type=2，也不能直接当作用户唯一索引。

隐藏 DB_ROW_ID 列的真实属性是：DD type=10（INT24 枚举）、char_length=6、hidden=2、collation_id=63、is_nullable=false、is_unsigned=false。这里的 DD type 和 unsigned 不能直接套普通 SQL MEDIUMINT 解码：它是六字节系统字段，不是三字节用户整数。先识别系统身份，再检查其固定属性，才是正确顺序。

元素 length=4294967295 是 DD 隐藏元素的标记，并不表示载荷有四十多亿字节。DB_ROW_ID 的物理宽度仍然固定为 6。实现同时核对列属性、索引隐藏状态、元素数量、顺序和方向，不依赖名字单独识别。

## 4. Schema 保留显式与隐式的区别

旧显式主键继续使用 `primary_key` 或 `primary_keys`，已有手工 schema 无需修改。无显式主键使用新字段：

```json
{"clustered_key":{"name":"u","columns":["id"]}}
```

表示用户 UNIQUE u 被选为聚簇键。其成员类型、方向和字符规则继续放在原 `columns` 定义中，最大键宽度等限制与显式主键相同。

```json
{"clustered_key":{"name":"PRIMARY","hidden_row_id":true}}
```

表示 SDI 的隐藏聚簇键。这里 `name` 是 DD 名称。两段仅展示键声明片段，完整 Schema 仍需用户列和 root/space/index ID。

`ClusteredKey` 与旧两个主键字段互斥。隐藏模式不能同时指定用户 Columns；完全省略所有键也不会自动启用隐藏模式。显式 `Read` 仍要求可信元数据，`ReadAuto` 才负责 SDI 发现。

`Record.RowID` 是 `*uint64`：隐藏键行有值，用户列聚簇键行是 nil。`Values` 始终只有 SQL 用户列，顺序不变。隐藏键的 `NodePointer.Key` 为 uint64，用户键继续按原类型返回。

## 5. 本阶段允许二级索引共存的确切含义

用户已确认放宽旧限制：普通 BTREE 二级索引存在时，可以读取其表的聚簇数据。`IndexMetadata.Clustered` 标识 SDI 首个索引角色，`Hidden` 保留 DD 隐藏状态；`Issues` 仍表达整体不支持或不一致问题。

`InspectTable` 报告所有索引入口，并核对各根页的 space/index ID、CRC、根页兄弟链等。它不会扫描二级索引记录。`ReadAuto` 返回的 `Pages`、`Nodes` 和 `Records` 来自聚簇树；二级索引有多少条记录、不访问的二级页是否完整，不在本阶段保证范围内。

FULLTEXT、SPATIAL、生成列、INSTANT 列布局等限制没有因此消失。索引元数据发生不支持或损坏时，也不会为了读行而默默忽略 Issues。

实现入口：[schema.go](../../schema.go) 定义三种身份与互斥校验；[metadata.go](../../metadata.go) 排除系统列、核对首个聚簇索引和其他根页；[record.go](../../record.go)、[tree.go](../../tree.go) 复用已有记录/树路径。下一章从真实字节定位 ROW_ID、事务字段和重复用户行。
