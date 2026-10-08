# 第30阶段生成列与不可见列夹具

14份MySQL8.0.45快照，独立临时库 `innodb_reader_generated_d64ef4eedc8a`。13份成功共1232行物化值，一份函数索引内部hidden=3列样本明确拒绝。原有411资产未改动。

使用 `scripts/generate_generated_fixtures.py` 在新库生成，每次DDL/DML完成后同一连接持有FOR EXPORT锁、复制文件并导出指定物化列SQL预期。环境、SHA256和行数见manifest.json；writer.sql.gz保存实际SQL，单表sql文件保存SHOW CREATE TABLE。SQL索引身份、官方SDI及独立预期均随文件保存。

类型来自生成器声明；生成表达式规范文本及INSTANT物理位置/生命周期/默认来自官方SDI的独立Python序列化。schema一致不是独立元数据正确性证据，SQL值和实际字节另行对照。隐藏ROW_ID表按多重集合验证，不依赖SELECT自然顺序。

`verify_generated_fixtures.py` 离线核对官方CRC/SDI及CLI，保存verification.json；physical.json来自仅物化读取结果，属于解析器派生摘要而非独立预期。全部14份CRC和28个SDI对象通过，严格入口/仅物化入口行为分别核对。

临时实例仅Unix socket且禁用网络，采集后关闭，未修改原实例或既有用户表。默认离线测试不需运行MySQL。细节及重现步骤见[第63章](../../docs/format/63-generated-column-layout.md)与[第64章](../../docs/format/64-generated-validation.md)。
