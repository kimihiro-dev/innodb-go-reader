# 第二十阶段真实索引生命周期夹具

2026-09-15通过scripts/generate_metadata_fixtures.py在独立新库innodb_reader_metadata_2b94e27b44eb生成。MySQL8.0.45、16KiB、crc32、DYNAMIC。原实例保持运行，未改全局配置或既有用户表。

- manifest.json：环境、采集时间、SQL索引身份、原始文件SHA256、预期可读性。
- generate.sql与各快照.sql：实际执行语句和SHOW CREATE TABLE原始输出（首字段为表名）。
- *.ibd：同一持久连接FOR EXPORT锁内复制的五个快照。
- *.expected.json与*.indexes.json：独立SQL行值及INNODB_INDEXES查询结果。
- *.sdi.json：官方ibd2sdi输出；*.report.json：Go InspectTable实际报告。
- verification.json：官方工具版本、严格crc32退出码、SDI对象数、CLI行数/退出码与错误。

默认执行：`GOCACHE=/tmp/innodb-go-build-cache go test -run '^TestMetadataLifecycle$' -v`。无需连接MySQL；重新采集可用INNODB_METADATA_FIXTURES选择新目录。

三个不含二级索引快照每个四行，两个含二级索引快照明确拒绝ReadAuto但能检查索引入口。重建后root仍为5，index ID从404变成405；这是页复用，不是主键根搬移。详细字节解释见[第44章](../../docs/format/44-metadata-roots-validation.md)。
