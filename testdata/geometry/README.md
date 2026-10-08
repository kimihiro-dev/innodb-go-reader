# MySQL 8.0.45 空间类型夹具

2026-09-16 从运行中的测试实例新建独立库 innodb_reader_geometry_c7eae7464cb2，使用同连接 FLUSH TABLES FOR EXPORT 保持导出锁采集。未改既有表或全局配置，实例保持运行。

12 组快照共 628 行，包括七类二维几何、一般/具体列声明、NULL、空和嵌套集合、SRID 0/4326、页外 LineString 和跨页树。manifest.json 记录配置、索引身份和解压后 SHA256；generate.sql.gz 为实际执行 SQL；各表 .sql 为 SHOW CREATE TABLE 输出，.json 为 SQL 元数据产生的手工 schema，.expected.json.gz 为独立 SQL 预期，.sdi.json.gz 为官方 ibd2sdi 输出，verification.json 记录官方 CRC/SDI/示例验收结果。

生成器：scripts/generate_geometry_fixtures.py，参数 --mysql、--socket、--out（新目录），密码仅经 MYSQL_PWD 环境变量提供。不要覆盖已有资产；新采集的事务 ID、space/index ID、LSN 和 SHA 会变化。

格式及逐字节解释见 docs/format/47-geometry-storage.md 与 48-geometry-validation.md；默认 go test 离线验证全部资产。
