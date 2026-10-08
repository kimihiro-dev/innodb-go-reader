# 第28阶段更新后的LOB夹具

17份稳定快照、54行SQL当前值，最终独立库 `innodb_reader_lob_updates_5d9c5681a7cc`。原始文件由SHA256固定，gzip仅用于节省存储。

生成器：`scripts/generate_lob_update_fixtures.py`。仅凭证环境变量MYSQL_PWD，不在文件中保存密码。两个会话分别完成写入/导出与保留旧读视图；所有写事务在导出前提交，释放视图后另采purge快照。

`manifest.json`保存实例配置、SQL JSON长度/空闲量和每快照统计；`verification.json`保存官方CRC/SDI与CLI/SQL验收。详细过程、边界和真实字节见[第59章](../../docs/format/59-updated-lob-layout.md)及[第60章](../../docs/format/60-lob-update-validation.md)。

包含TEXT/BLOB主键迁移继承、全量替换、增长缩短、NULL/空值和删除，以及页内/页外JSON小修改/块替换、多轮历史链、空洞/重用、整值增长、删除与purge。JSON SQL预期直接保存原始查询文本，不经浮点转换。CHAR表达式产生的binary opaque在普通JSON视图中为base64:type250，属于预期SQL行为。
