# MySQL 5.7明确补丁的独立夹具

本目录保存5.7.9、5.7.10、5.7.11、5.7.12、5.7.28、5.7.43的真实独立SQL元数据及FOR EXPORT快照。每个版本下base和secondary分别来自既有[类型生成器](../../scripts/generate_mysql57_fixtures.py)和[二级生成器](../../scripts/generate_mysql57_secondary_fixtures.py)，每组22份；既有5.7.44夹具不改写。

每组manifest.json记录准确案例、行数、文件字节数和解压SHA256，source.json保留准确运行版本/页大小/checksum、SQL列/键及系统字典身份。schema/indexes是独立Python工具转换的可信布局；expected.json.gz及secondary.json.gz来自服务器SQL，不是Go解析结果。writer.sql.gz记录实际执行SQL；.sql保留SHOW CREATE TABLE输出，.ibd.gz仅无损压缩物理文件。

全部输入为16 KiB、Single、非压缩非加密、strict_crc32、DYNAMIC/COMPACT，使用专用随机数据库。SQL采集与备份锁在独立工具中，产品Schema命令不采集快照。新增目录多一层，不加入原首版自动SDI扫描矩阵；独立[补丁测试](../../mysql57_patch_test.go)覆盖Read/Scan/LOB/聚簇与二级SQL查询，[空间测试](../../space_test.go)递归包含所有新文件。[Schema测试](../../internal/mysql57/collect_test.go)逐份对照模型及原始来源。

每版本44份快照、2208行、38棵二级及232组查询预期；合计264份、13248行、228棵二级及1392组查询。隐藏ROW_ID只比较可见列多重集合及定位一致性；不把隐藏字段称为SQL预期。空表、精确数值、NULL/空值、Unicode/字符集、空间值、JSON、旧LOB/DML和生成列均保留既有验收组合，不推出全宽度/全类型键或未测补丁兼容。

源码/镜像来源、TLS和实际执行检查见[第92章](../../docs/format/92-mysql57-patch-compatibility.md)及[本轮证据](../../docs/mysql57-patch-validation/README.md)。复验需新建独立输出目录和测试实例，准确数据库/内部ID由各实例分配，不能期待重新生成的文件SHA与本轮相同。
