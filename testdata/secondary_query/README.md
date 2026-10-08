# 第35阶段二级查询快照

MySQL8.0.45、16KiB、crc32、file-per-table。2026-09-21在隔离临时实例的新库 `innodb_reader_secondary_query_bdd66c509cc1` 生成，通过 FLUSH TABLES FOR EXPORT 采集，实例随后正常关闭。既有用户表和历史夹具未改。

- prefixes：480行、270查询，UTF-8 name(3)/rank DESC、NULL/空串/前缀碰撞与页外TEXT。
- covering：COMPACT，3000行、76查询，可空二级整数、超过2^53的BIGINT主键和非覆盖列。
- huge：2行、12查询，16777217字节LONGBLOB。完整读取明确拒绝；未选payload的覆盖和回表查询正常。

`manifest.json` 保存原始ibd的SHA256和采集环境；`.queries.json.gz` 保存查询参数、独立SQL及精确结果。SQL谓词比较完整原列，ORDER BY 使用存储前缀/混合方向/完整定位后缀，NULL采用明确展开；不将完整值排序误当物理索引顺序。两份可完整读取的快照另存expected，huge有意不存完整成功预期。

`verification.json` 记录3文件官方CRC/SDI及358条查询CLI对照，共27340行。查询间结果会重复，不能作为独立表行数。测试显式提高共享MaxEntries至10000000，单次回表不重置预算。

生成与验证脚本位于 `scripts/generate_secondary_query_fixtures.py`、`scripts/verify_secondary_query_fixtures.py`；新采集必须另用新库和目录。详细字段字节、来源、延迟LOB和限制见[手册73](../../docs/format/73-secondary-query-navigation.md)及[74](../../docs/format/74-projection-and-lookup-validation.md)。
