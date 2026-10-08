# 第38阶段分区集合夹具

2026-09-29在用户授权MySQL8.0.45测试实例的新独立数据库`innodb_reader_partition_9e39b58bcfab`采集。实例为16KiB、file_per_table=1、crc32；只设置生成会话的sql_require_primary_key=OFF以建立无主键样本，未改全局配置、原有用户表，也未停止用户实例。

`manifest.json`保存环境、每文件解压大小/SHA及支持状态。34份`.partition.gz`按12组集合组织，11组成功3184行，1组子分区30行明确拒绝。集合文件不属于普通单文件`.ibd.gz`测试glob；空分区也必须保留。各生命周期快照是不同采集时刻，不应随意混配。

- `*.partition.gz`：FLUSH TABLES FOR EXPORT持锁期间复制的原始物理文件，gzip不改变解压字节。
- `*.expected.json.gz`：SQL全表`ORDER BY id,n`结果和逐分区同序结果；hidden_rows按用户列值验证，不把ROW_ID当作SQL字段。
- `*.sql-metadata.json.gz`：PARTITIONS、INNODB_TABLES、INNODB_INDEXES查询及SHOW CREATE TABLE原始结果。
- `*.sdi.json.gz`：官方8.0.45 ibd2sdi结果，保持对象键/完整JSON。
- `writer.sql.gz`：生成及采集SQL，包含一次被会话主键要求拒绝的建表尝试和后续恢复过程；不含密码。
- `verification.json`：独立验证脚本的官方CRC/SDI对象数、SQL表身份、官方索引身份及JSONL/CSV导出统计。

本版本INNODB_INDEXES跳过逻辑索引空物理属性，分区索引查询结果实际为空；不补造SQL索引预期。物理ID/root/space用官方ibd2sdi分区索引独立核对。Go测试额外逐对象对照官方SDI，损坏测试仅修改内存副本。

复跑离线验证：

```sh
python3 scripts/verify_partition_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
go test -run TestPartition ./...
```

新采集只能写入新目录、创建新唯一库；不要覆盖现有夹具：

```sh
python3 scripts/generate_partition_fixtures.py --mysql /path/to/mysql --socket /path/to/mysqld.sock --out /tmp/new-partition-fixtures
```

密码经交互提示进入客户端进程环境，不保存到SQL日志。`--resume`仅用于本脚本标记incomplete的同一输出目录；先检查失败位置，不能用它重写已完成快照。采集提交后的测试数据；生成脚本不承诺从任意活动生产库获得一致性快照。

布局、字节及案例见[第79章](../../docs/format/79-partition-collection-layout.md)和[第80章](../../docs/format/80-partition-validation-and-cli.md)。
