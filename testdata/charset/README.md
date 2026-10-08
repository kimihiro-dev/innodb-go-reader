# MySQL 8.0.45 字符集夹具

2026-09-16 采集自新独立库 innodb_reader_charset_c3a4f995f69a。保持同连接 FLUSH TABLES FOR EXPORT 锁期间查询预期并复制，未改既有表或全局配置，原实例保持运行。

21 个快照、1064 行，utf8mb4/utf8mb3/ascii/MySQL latin1，九个排序规则、全部128 ASCII/256 latin1字节、单字节定长CHAR和UTF8变长CHAR、四类零宽字段、ENUM/SET、长度边界、八个真实页外字段及600行跨页树。

manifest.json 保存环境、space/index/root及解压后SHA256；generate.sql.gz 保存实际SQL；.sql为SHOW CREATE TABLE输出；.json为独立schema；.expected.json.gz保存SQL HEX/CONVERT/字符数/字典数值以及PAD_CHAR_TO_FULL_LENGTH完整CHAR视图；.sdi.json.gz为官方SDI输出；verification.json记录官方CRC/SDI/CLI对照。

生成器 scripts/generate_charset_fixtures.py，参数 --mysql/--socket/--out（新目录），密码仅从 MYSQL_PWD 读取。新增采集不要覆盖既有资产；数据库ID、事务信息及哈希会变化。SQL模式仅在生成会话调整。

详见 docs/format/49-charset-storage.md 与 50-charset-validation.md；默认Go测试离线验证。
