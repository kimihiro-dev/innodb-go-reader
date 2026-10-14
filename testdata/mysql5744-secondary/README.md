# MySQL 5.7.44 二级索引固定证据

本目录为阶段42 B独立采集资产，不改写A批或8.0夹具。版本/配置来自各source.json，物理身份来自实际系统字典；精确采集时间、专用库名和解压后SHA256见manifest.json。生成入口是[scripts/generate_mysql57_secondary_fixtures.py](../../scripts/generate_mysql57_secondary_fixtures.py)。

每表在同一FOR EXPORT锁内采集Schema、SecondarySchema、原始字典/STATISTICS、独立SQL预期和物理副本；文件使用gzip无损保存。读取期间保证稳定，FOR EXPORT只服务受控测试采集，不提供读取器的在线事务恢复能力。

| 文件 | 含义 |
|---|---|
| NAME.ibd.gz | 原始物理文件；manifest的sha256对解压结果计算 |
| NAME.json | 聚簇Schema，沿用A批模型 |
| NAME.indexes.json | SecondarySchema数组，声明前缀与完整聚簇定位 |
| NAME.source.json | SQL列、原始SYS_INDEXES/SYS_FIELDS、STATISTICS、环境和实际文件位置 |
| NAME.expected.json.gz | 存储用户列SQL结果；二进制HEX，VIRTUAL不求值 |
| NAME.secondary.json.gz | 二级物理可见字段SQL值及各查询的独立完整值投影 |
| NAME.sql | 服务端SHOW CREATE TABLE输出 |
| writer.sql.gz | 建表/写入/采集/SQL期望查询的完整会话语句 |

普通/唯一/NULL/重复键、前缀/复合/定位去重、隐藏ROW_ID、实际唯一聚簇、生成列、三层二级树、空树及四个_bin字符集分别以DYNAMIC/COMPACT采集。5.7忽略DDL中的DESC，采集使用实际ASC。三层二级树800行；节点和行独立核对SQL。

SQL无法直接读取DB_ROW_ID。隐藏表的物理项与查询按可见用户字段多重集合比较；ROW_ID字节与回表定位来自物理记录的一致性验证，不能冒称独立SQL身份预期。其他表按实际物理键的SQL排序比较。范围SQL显式处理NULL和完整键的词典序，不把前缀的候选等同完整值。

这些原件只读，损坏实验修改副本。格式说明见[第88章](../../docs/format/88-mysql57-secondary-indexes.md)，验证报告见[原始日志](../../docs/mysql57-validation/secondary/report.json)。代表组合不推定未采集类型/宽度、全部DDL/DML、其他补丁或分区兼容。
