# 12. 验证变长字段、页内碎片与页外边界

学习目标：从独立 SQL 预期验证字段完整性；理解 free 链与 garbage 不是同一个概念；识别“有长度元数据”不等于“当前页拥有完整值”。前置内容为第 06–08、11 章。

<a id="1-交付夹具"></a>

## 交付夹具

固定资产位于 `testdata/variable`。数据、生成 SQL 和 SQL 预期用 gzip 保存；manifest 的 SHA256 对原始解压文件计算。

| 名称 | 行数 | 根层级 | 验收目标 |
|---|---:|---:|---|
| length_rows | 10 | 0 | 0/1/127/128/255/256/300/1024 字节、短/长声明、NULL、空值、任意二进制 |
| bitmap_rows | 3 | 0 | 10 个可空变长列、跨字节位图、NULL 跳过长度、主键在最后 |
| variable_tree | 350 | 1 | 固定种子乱序、中文/emoji、混合长度、60 个叶子与根页 |
| large_declared | 2 | 0 | VARCHAR(12000) 与 VARBINARY(16000)，6000+1000 字节页内数据及空值 |
| empty_variable | 0 | 0 | 空表仍可以按新 schema 扫描 |
| external_text | 2 | 0 | 页外 VARCHAR 必须报不支持 |
| external_binary | 2 | 0 | 页外 VARBINARY 必须报不支持 |

前五组 365 行必须完整还原；后两组共 4 行是拒绝测试，不能计作已还原数据。加上之前 20 组、17083 行，当前是 25 组成功样本、17448 行逐列 SQL 对照，以及 2 组拒绝样本。

最终专用测试库为 `innodb_reader_fixture_00d36aeb9abc`。生成器只新建唯一命名数据库，没有修改已有表。所有新文件均通过 MySQL 官方 innochecksum。

<a id="2-预期值从哪里来"></a>

## 预期值从哪里来

`scripts/generate_fixtures.py --variable` 在同一个 mysql 客户端会话中：

1. 建表和插入，保存执行的完整 SQL。
2. 持有全部测试表的 FOR EXPORT 锁。
3. 获取 SHOW CREATE TABLE、索引根页/space/index ID，以及按主键排序的 SQL 结果。
4. 复制 .ibd；完成后 UNLOCK，连接关闭也能释放锁。

文本和整数仍通过 JSON_ARRAY 获取；二进制列改用 HEX(column) 放入预期 JSON。HEX(NULL) 保持 NULL，空二进制对应空字符串。这避免让任意字节经过字符集转换。

测试将 SQL HEX 解码为字节，与 Go []byte 逐字节比较；再将 Go 值编码为 JSON，Base64 解码后再次比较。SQL 预期不是调用 Go 解析器产生的。整数仍使用 UseNumber 保持精度。

<a id="3-真实碎片free-链为空garbage-仍有-2814-字节"></a>

## 真实碎片：free 链为空，garbage 仍有 2814 字节

`variable_tree` 页 8 的关键页头字段：

| 页内偏移 | 长度 | 值 | 含义 |
|---:|---:|---:|---|
| 40 | 2 | 11198 | PAGE_HEAP_TOP |
| 42 | 2 | 0x8005 | COMPACT 标记，5 个堆记录含系统记录 |
| 44 | 2 | 0 | PAGE_FREE 无链头 |
| 46 | 2 | 2814 | PAGE_GARBAGE |
| 54 | 2 | 3 | 三条活动用户记录 |

为什么没有 free 记录，却还有垃圾字节？

[page0cur.cc:1276–1303](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/page/page0cur.cc#L1276) 在插入时尝试复用 free 链头，只要旧记录空间足够即可；[page0page.ic:786–811](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/page0page.ic#L786) 将链头移到下一条，同时只从 garbage 扣除新记录需要的字节。

因此，一个较大的 free 记录被较小的记录复用后，可以留下零散字节；即便链表耗尽，计数也不一定为零。如下是原理示意，并非该页的插入历史重放：

```text
原 free 空间 1000 字节 → 新记录占 300 字节 + 遗留碎片 700 字节
free 链摘除原记录，garbage 只减少 300
```

页 8 的实测数据符合这一格式规则；我们没有读取 redo 来重建它的逐次操作历史。

修正后的校验为：

- free 非零时，garbage 不能为零；free 为零时允许存在碎片。
- 活动记录的总字节数必须等于 `heap_top−120−garbage`，依据 [page_get_data_size:770–778](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/include/page0page.ic#L770)。
- 活动/free 链仍检查循环、堆编号与重叠；碎片不当作用户行，也不要求每个碎片都有记录头。

本页的三条活动记录合计 `11198−120−2814=8264` 字节。测试保留这个真实场景，并把垃圾计数增加 1，验证错误计数仍会被拒绝。

<a id="4-页外字段为什么必须停止"></a>

## 页外字段为什么必须停止

`external_text` 的第一行 id=0 存放短文本 inline，第二行 id=1 存放 10000 个 emoji，共 40000 字节。第二行位于根页 4，origin=158，Start=150。真实元数据为：

```text
页内 150: 14 c0 | 00 | 00 00 18 ff d2
           长度  NULL      记录头
```

先读取 c0：0x80 表示双字节，0x40 表示外部存储；再读 14。去掉标志得到本地长度 20，而不是实际字符串长度 40000。

本地数据部分在主键与 13 字节系统字段之后，页内偏移 175，真实 20 字节为：

```text
00 00 00 4b 00 00 00 05 00 00 00 01 00 00 00 00 00 00 9c 40
```

这里是页外引用，不能转成 20 字节字符串返回。本阶段只识别标志并拒绝，不解读引用字段、不访问它指向的页，也不声称完成 LOB 恢复。

`external_binary` 第二行同样使用长度字节 `14 c0`，origin=154。其 30000 字节二进制也明确返回 ErrUnsupported。

两张表都故意先放一条可解码短行，再放页外值。`Read` 必须返回 nil 结果及错误，不能把前一条当成成功扫描的整张表返回。

<a id="5-实现入口与错误验证"></a>

## 实现入口与错误验证

- `schema.go`：区分 max_chars / max_bytes，限定支持类型与整数主键。
- `variable.go`：一/两字节长度、声明上限与 external 标志。
- `record.go`：逆向元数据读取、NULL 与二进制值、活动字节核算。
- `page.go`：允许 free 链为空时存在碎片。
- `variable_test.go`：七组固定样本、SQL/HEX/Base64 对照、长度损坏、类型属性、UTF-8、字段截断、切片所有权、碎片计数与 fuzz。

单元测试明确覆盖：M=255/256 时对 80、ff 的不同解释；127/128、255/256 边界；双字节按低地址存放为 `00 81`；长度字节缺失；超过声明上限；两字节编码过短；页外标志；NULL 与空二进制不同。

旧夹具全部保留。Go 的错误返回不等于能发现任意损坏：读取路径尚未做 CRC，schema 仍是可信输入；官方页校验是固定资产的独立验证。

<a id="6-复跑与示例输出"></a>

## 复跑与示例输出

在项目根目录：

```sh
go test ./...
go test -race ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzVariableTree$' -fuzztime=10s -parallel=2
gzip -dc testdata/variable/length_rows.ibd.gz > /tmp/length_rows.ibd
go run ./examples/read /tmp/length_rows.ibd testdata/variable/length_rows.json
```

输出 10 行 JSON；二进制字段是 Base64。最后一行的原始文本包含 NUL，JSON 使用转义保存；二进制 HEX 预期在 `.expected.json.gz` 中，不能直接将 HEX 字符串与 Base64 字符串比较。

重新生成可使用下面命令。先安全设置 MYSQL_PWD 环境变量，输出目录必须不存在，密码不要写入文件：

```sh
python3 scripts/generate_fixtures.py --variable \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-variable-fixtures
```

重新生成的事务信息、表空间编号及哈希可能变化。本章字节、偏移和数据库名对应本次交付文件，不能当作格式常量。

第四阶段完成后，下一步可以从本章的真实页外样本出发，研究引用布局和 LOB 页的数据还原；当前支持边界仍是完整页内字段。
