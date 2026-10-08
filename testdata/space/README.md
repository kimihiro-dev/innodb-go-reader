# 第36阶段空间分配快照

2026-09-22，新独立库 `innodb_reader_space_2b493d901bb5`，MySQL8.0.45、16KiB、crc32、file-per-table、DYNAMIC。使用用户授权的本地实例，经FOR EXPORT保存；仅操作新库，既有表和全局设置未改，实例保持运行，密码未写入项目。

四个不可修改的原始快照使用`.space.gz`，专用于分配分析，不加入既有逐行/点查矩阵：

| 名称 | SQL行 | 文件页 | 目的 |
|---|---:|---:|---|
| empty | 0 | 8 | 小于64页的表及合法全零空闲页 |
| grown | 17000 | 17408 | 跨XDES组、状态5租用区、两棵用户索引和17000个LOB页 |
| deleted | 10 | 17408 | 释放后文件不缩小、空闲页保留旧类型/数据 |
| rebuilt | 10 | 18 | 重建更换space/index身份并缩小文件 |

manifest记录SHA/字节数/行数和采集环境，writer.sql.gz为完整生成SQL。SQL元数据预期、官方CRC/页统计/SDI、Go报告留档和verification结果分别保存；Go报告不作为独立预期。官方innochecksum的Other类型包含新式LOB，其页类型dump不会逐页列出这些页，验证器单独核对汇总。

4文件34842页通过官方工具、独立XDES位图分类和SQL索引身份对照。已有487份资产另做完整空间回归；它们未修改。生成器和验证器位于scripts目录，后续生成应使用新库、新目录。布局与分母见[手册75](../../docs/format/75-space-allocation-layout.md)和[验收76](../../docs/format/76-space-analysis-validation.md)。
