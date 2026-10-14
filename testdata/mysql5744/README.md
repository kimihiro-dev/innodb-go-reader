# MySQL 5.7.44 固定验证资产

2026-10-13 在专用 Docker 容器以 linux/amd64 镜像生成；镜像 manifest 为 `sha256:dab0a802b44617303694fb17d166501de279c3031ddeb28c56ecf7fcab5ef0da`。准确库名、版本、配置和每个文件SHA256分别保存在 manifest/source。首批22份稳定快照、526条快照行，生成与采集期间持有FOR EXPORT锁；不修改既有8.0资产。

每组包含原始 `.ibd.gz`、既有 Schema `.json`、独立 SQL `.expected.json.gz`、原始系统/列/索引字典 `.source.json` 和 SHOW CREATE TABLE `.sql`。writer.sql.gz记录实际执行SQL，包含锁定/解锁；较大文件含已释放或未初始化空间，gzip只压缩封装，不改写物理字节。manifest的SHA256对解压文件计算。

| 样本前缀（各有DYNAMIC/COMPACT） | 验证范围 |
|---|---|
| scalar | 整数边界、精确DECIMAL、浮点、BIT64、fsp6、零值、ENUM/SET、JSON、SRID/WKB、NULL/空值 |
| lob initial/grown/shrunk_deleted | 多列多页旧BLOB、VARCHAR/VARBINARY页外、完整替换及缩短/删除 |
| families | 全部TEXT/BLOB类型的代表值 |
| tree | 实际三层复合VARBINARY/INT聚簇树；5.7忽略DDL中的DESC，采集实际ASC顺序 |
| hidden / unique | GEN_CLUST_INDEX和实际唯一非空聚簇键 |
| generated | STORED物化与VIRTUAL单独报告 |
| charset | ascii、MySQL latin1、utf8，scalar/lob补充utf8mb4 |
| empty | 空表单叶根 |

SQL预期中的YEAR/BIT显式转换为unsigned，避免5.7 JSON_ARRAY的YEAR opaque表示。二进制使用HEX，DECIMAL和日期时间使用字符串，几何使用官方SRID/WKB，JSON保留原生值；隐藏ROW_ID样本按无序多重集比较。SQL预期不来自Go解码器。

离线复验：

```sh
go test . -run 'TestMySQL57|TestScanAllFixtures|TestQueryFixtureMatrix|TestSpaceAssets' -count=1
go test ./internal/cli -run 'TestExplicitSchema|TestSchemaUnicodeAlias' -count=1
go test ./rowio -run TestAllFixtures -count=1
```

重新生成时使用新的输出目录和独立库，保留本目录原始文件：

```sh
python3 scripts/generate_mysql57_fixtures.py --container CONTAINER --out /tmp/mysql57-new-fixtures
```

采集已有运行实例的用法见[CLI指南](../../docs/CLI.md#mysql-5744显式-schema)，格式解释见[第87章](../../docs/format/87-mysql57-explicit-schema.md)。B批二级/索引钻取及分区集合不由本组验收推定。
