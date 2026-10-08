# 第二十一阶段JSON真实夹具

来源：MySQL8.0.45，专用新库innodb_reader_json_2bf7dabe3618。5表636行，同一生成会话持有FOR EXPORT锁复制原始文件，gzip无损保存。SQL、DDL、环境、原始SHA256、预期随目录交付；密码不存入资产。

- manifest.json记录环境、原始文件哈希和索引身份；generate.sql.gz为实际生成SQL日志。
- *.expected.json.gz为SQL显示、根类型、深度和存储字节数；数字须精确比较。
- json_opaque.types.json为独立补采的子节点SQL JSON_TYPE证据。
- *.sdi.json.gz为官方ibd2sdi输出，verification.json记录官方工具版本及CLI对照结果。
- 默认go test运行全部夹具，json_test.go还覆盖合成边界和损坏输入。

源码生成器：[generate_json_fixtures.py](../../scripts/generate_json_fixtures.py)。详细格式与真实字节：[第45章](../../docs/format/45-binary-json.md)、[第46章](../../docs/format/46-json-opaque-validation.md)。TIMESTAMP标签、VAR_STRING特例和内部81位decimal的测试为合成证据，本批真实时间节点仅DATE/TIME/DATETIME。
