# 第29阶段INSTANT真实夹具

17份快照3916行，MySQL8.0.45独立库 `innodb_reader_instant_e85d0f225972`。

生成器为 `scripts/generate_instant_fixtures.py`，每次写入/DDL结束后FOR EXPORT采集，原文件SHA256保存在manifest.json，SQL预期、DDL、官方SDI及完整writer.sql.gz随夹具交付。密码只由MYSQL_PWD环境变量传入，未写入文件。verification.json记录官方CRC/SDI/CLI与SQL结果。

类型声明独立保存在生成器中；显式schema的生命周期、物理位置和默认原字节从官方SDI用独立Python序列化，不能称为第二份独立元数据。独立值验收来自SQL，另有行版本/默认来源/记录字节及损坏测试。

覆盖多轮ADD/DROP、FIRST/AFTER、默认改变、同名重增、已DROP的LOB、隐藏ROW_ID、NULL位图跨字节、多页树、精确类型默认、真实版本64和重建清零。旧式INSTANT/升级混合格式未作为成功样本。

详细学习和复现见[第61章](../../docs/format/61-instant-row-layout.md)与[第62章](../../docs/format/62-instant-validation.md)。
