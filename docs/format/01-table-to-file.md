# 01 从表到文件

学习目标：理解表、表空间、索引、页、记录和字段的关系，并知道我们的测试文件来自哪里。前置知识只需 SQL 表与文件字节的概念。

## 1. 从 SQL 中的一行出发

主案例实际建表语句保存在 `testdata/mysql8045/lesson_rows.sql`，核心定义如下：

```sql
CREATE TABLE lesson_rows (
    id INT NOT NULL,
    score INT NULL,
    name VARCHAR(32) NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB ROW_FORMAT=DYNAMIC DEFAULT CHARSET=utf8mb4;
```

按顺序插入 `(7,0,'')`、`(-3,42,'InnoDB')`、`(12,-5,'你好')`、`(2,NULL,NULL)`。

在这里，`SELECT` 给你三个逻辑列；物理文件里还必须存储定位和解释这些值的结构，例如页头、记录头、变长长度和事务字段。文件不是将 SELECT 文本逐行写到磁盘。

## 2. 各层结构如何关联

```text
lesson_rows 表
  └─ 独立表空间 lesson_rows.ibd
       ├─ 表空间管理等页面
       ├─ SDI 字典页面（本阶段不解码）
       └─ PRIMARY 聚簇索引
            └─ 第 4 页：根页，同时也是叶子页
                 ├─ 页头与页目录
                 ├─ infimum / supremum 系统记录
                 └─ 用户记录 → id、系统字段、score、name
```

InnoDB 聚簇索引存放行数据。对于我们的显式主键表，它按主键组织记录；将来表变大，根页和内部页用于导航，行数据位于叶子页。[官方索引说明](https://dev.mysql.com/doc/refman/8.0/en/innodb-indexes.html)

“只有一个叶子页”不等于“文件只有一个页”。主案例文件为 114688 字节，共 7 个 16384 字节的页。空间里还有管理信息和其他页面；第一步不需要逐个完整解码它们才能读取这张表。

## 3. 为什么仍然输入表结构

字节 `80 00 00 07` 本身不会告诉我们它是四字节有符号整数、字符串的一部分，还是其他结构。当前程序从 JSON 接收列顺序、类型、是否可空、字符长度与主键信息。

主案例元数据 `lesson_rows.json` 中还有：

| 属性 | 此次样本值 | 来历 |
|---|---:|---|
| root_page | 4 | `INFORMATION_SCHEMA.INNODB_INDEXES.PAGE_NO` |
| space_id | 40 | 表空间标识 |
| index_id | 249 | PRIMARY 索引标识 |

这几个数字不是算法内置常量。`Read` 在文件里核对页号和 ID，但不读取 SDI，因此无法证明你为任意文件填写的列定义正确。第一步通过受控建表过程保证这份输入可信；自动发现元数据留待后续实现。

## 4. 稳定快照为什么必要

运行中的 MySQL 可能有尚未刷盘的数据，也可能正在改变页。直接复制活跃文件，再拿一个不同时间的 SELECT 比较，不能形成可靠验证。

生成器只操作唯一命名的专用数据库：建立表并提交插入后，在**同一个持续存活的 mysql 客户端会话**中：

1. 对全部夹具表执行 `FLUSH TABLES ... FOR EXPORT`。
2. 保持会话和锁，读取索引元数据、SHOW CREATE TABLE 和排序后的 SQL 结果。
3. 在锁尚未释放时复制 `.ibd` 到项目目录。
4. 完成复制后执行 `UNLOCK TABLES`。

异常时关闭连接也会释放锁。生成器不会删除数据库或覆盖已有输出目录。官方对导出锁和复制时机的说明见 [表空间导出流程](https://dev.mysql.com/doc/refman/8.0/en/innodb-table-import.html)。这里没有执行 IMPORT，也不需要 `.cfg` 来解码记录。

## 5. 保存了哪些可复核材料

目录 `testdata/mysql8045/` 包含：

| 文件 | 作用 |
|---|---|
| `generate.sql` | 实际执行的建库、建表和插入 SQL |
| `manifest.json` | 版本、配置、快照方式、行数、ID、文件长度和 SHA256 |
| `表名.sql` | 完整 SHOW CREATE TABLE |
| `表名.json` | 输入解析器的 schema 和根页元数据 |
| `表名.expected.json` | 锁保护下 SQL 查询得到的所有列值 |
| `表名.ibd` | 真实 MySQL 文件副本 |

共七组：4 行主案例、空表、1 行单行表、6 行边界表、2 行多字节 NULL 位图表、40 行多目录槽表、4 行多变长字段表。没有保存密码。

## 6. 自己验证这一章

```sh
python3 -c 'from pathlib import Path; p=Path("testdata/mysql8045/lesson_rows.ibd"); print(p.stat().st_size, p.stat().st_size//16384)'
```

结果：`114688 7`。接着阅读 `lesson_rows.sql`、`.json` 与 `.expected.json`，确认同一张表的逻辑结构、入口元数据与期望值。

代码对应：`scripts/generate_fixtures.py` 的 `query` 和导出锁区间；`schema.go` 的 `Schema`、`Column` 与 `validate`。解析器自身不运行 SELECT，只有造夹具阶段使用 SQL 作为独立基准。

边界：这些样本均是新建、插入后静止的表。本阶段没有证明事务快照恢复、删除记录处理、任意 DDL 历史或全类型兼容性。
