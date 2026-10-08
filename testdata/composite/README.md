# MySQL 8.0.45 复合整数主键夹具

2026-09-17 采集自新独立库 innodb_reader_composite_25c6d3d89012。每表使用同连接 FLUSH TABLES FOR EXPORT 锁定期间查询并复制快照；未修改既有表或全局配置，原实例保持运行。

6快照3614行：空表、非连续主键声明位置、五种整数宽度及符号组合、64位端点、三个成员共享前缀、600行随机插入树、16列键3000行真实三层树及一处60000字节页外文本。

manifest.json 保存环境、space/index/root、解压后SHA256；generate.sql.gz为实际SQL；.sql是SHOW CREATE TABLE，.json是显式schema，.expected.json.gz为SQL声明列序/全主键ORDER BY结果，.sdi.json.gz为官方SDI，verification.json为CRC/SDI/CLI验收。

生成器 scripts/generate_composite_fixtures.py，参数 --mysql/--socket/--out（新目录），密码仅来自MYSQL_PWD。新采集不要覆盖原资产；事务、文件哈希及内部ID可能变化。格式学习见 docs/format/51-composite-key-layout.md、52-composite-key-validation.md，默认Go测试无需在线实例。
