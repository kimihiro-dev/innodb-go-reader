# 18. DECIMAL 混合记录与全精度验证

<a id="学习目标"></a>

## 学习目标与前置概念

将第 17 章的七字节小数放回完整记录，理解为什么 NULL、变长元信息、主键重排和页外 TEXT 不会互相混淆；然后复现所有合法 precision/scale 的 SQL 对照。

## 一条混合记录的完整布局

`decimal_lesson` 的 SQL 声明顺序为：

```text
amount DECIMAL(14,4) NULL
note   VARCHAR(40) NULL
id     INT PRIMARY KEY
tiny   DECIMAL(2,2) NULL
body   TEXT NULL
```

id 的物理位置排到最前，其后是 13 字节系统字段，再按声明顺序排其他列。四个可空列的位图按 amount、note、tiny、body 分配 bit 0..3。只有 note/body 需要变长长度项。

以下为真实页 4 的记录，偏移均为页内十进制：

| id | Start | origin | End | 元信息原始字节 |
|---:|---:|---:|---:|---|
| 1 | 120 | 127 | 158 | `06 08 0000100028` |
| 2 | 158 | 167 | 218 | `14c0 06 00 000018003b` |
| 3 | 218 | 226 | 254 | `00 03 00 0000200022` |
| 4 | 254 | 260 | 277 | `0f 000028ff6c` |

id=1 的 body 为 NULL，所以位图是 08；note 为“正数”，UTF-8 六字节，长度区只有 06。完整记录为：

```text
120: 长度 06
121: NULL 位图 08
122..127: 五字节记录头
127..131: INT 主键
131..144: DB_TRX_ID + DB_ROLL_PTR
144..151: amount 七字节
151..157: note 六字节
157..158: tiny 一字节
body: NULL，无长度项，无值字节
```

区间均为左闭右开。记录共 `1+1+5+4+13+7+6+1=38` 字节，与 End−Start 相符。

id=2 的 body 为 60000 字节“界”文本，存放在页外。本地长度区从靠近位图的方向读到 note 的 06，再读 body 的 c0 和 14：external=1，本地引用长度为 20。两个 DECIMAL 均不占长度项。值区依次是 4 字节主键、13 字节系统字段、7 字节 amount、6 字节 note、1 字节 tiny、20 字节 body 引用。记录本地总长 60 字节；完整 body 由已有 LOB 解析接着还原。

id=3 的 body 是空串而非 NULL，因此仍有 00 长度项。amount 的七字节零返回 `"0.0000"`，tiny 返回 `"0.00"`。id=4 位图 0f，所有普通列均为 NULL，origin 后只剩 17 字节主键与系统字段，不能把后续空闲页字节误读成 DECIMAL 零值。

## 真实夹具矩阵

生成器将 MySQL DECIMAL 通过 `CAST(column AS CHAR)` 转成 SQL 字符串后构造 JSON 预期。整个采集、保存、测试对照过程不经过浮点转换。物理文件则在同一 mysql 会话持有 FOR EXPORT 锁时复制，预期 SELECT 在锁释放前完成。

| 夹具 | 数量 / 行数 | 覆盖 |
|---|---|---|
| decimal_matrix_0..15 | 16 表，各 6 行，共 96 行 | 全 1580 种合法 P/S；正负最大值、零、正负最小步长、NULL |
| decimal_unsigned | 1 表，4 行 | DECIMAL(65,30)、DECIMAL(30,30) UNSIGNED、零/NULL/极值 |
| decimal_lesson | 1 表，4 行 | 小数、主键位于中间、中文、空串、NULL、60000 字节页外 TEXT |
| decimal_tree | 1 表，600 行 | 乱序插入、多页 root level=1、BIGINT 主键、65 位小数/整数、长中文 |

合法组合数为 `sum(P=1..65)(min(P,30)+1)=1580`。每批最多一百个可空 DECIMAL，位图跨越多个字节；主键放在声明中间，验证物理重排。矩阵按 SQL 实际值对照，不依赖解析器自己的编码器生成预期。最大宽度三十字节、各残组宽度与整数位数为零的情况均覆盖。

专用测试库为 `innodb_reader_fixture_7044725c1e7c`。只新增该库，未修改既有用户表。19 个新物理文件、建表 SQL、生成 SQL、schema、预期与 SHA256 均在 `testdata/decimal`，共 704 行成功还原。全项目累计 54 组成功夹具、18264 行；加上第六阶段保留的超限拒绝夹具，共 55 组资产。

## 非法输入如何拒绝

测试修改内存中的夹具副本，保持交付原文件不变：整数残组超过声明位数、九位组达到十亿、小数残组达到 10000、将真实负值声明为 UNSIGNED、截断字段可用区域。Read 返回 ErrCorrupt 且结果为 nil，不能返回已经解出的前几行。

独立解码测试补充缺字节/多字节、纯小数、正负零、输入字节不被修改；schema 测试覆盖缺 precision、越界 scale、错误属性和 DECIMAL 主键拒绝。对于负零编码，本项目统一输出非负零；这是一项输出规范，不表示夹具 SQL 存在单独的负零值。

仍依赖可信外部 schema：相同字节数的错误 P/S 可能无法仅凭文件辨认。没有自动元数据解析前，不宣称能检测所有 schema 不匹配。页 CRC 当前仍由独立 innochecksum 检查，不在 Read 内校验；任意合法编码的字节变动也不保证能被结构检查发现。

## 复现与代码入口

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzDecimal$' -fuzztime=10s -parallel=2
gzip -dc testdata/decimal/decimal_lesson.ibd.gz > /tmp/decimal_lesson.ibd
go run ./examples/read /tmp/decimal_lesson.ibd testdata/decimal/decimal_lesson.json
```

示例第一行应是：

```json
["1234567890.1234","正数",1,"0.01",null]
```

重建使用 `scripts/generate_fixtures.py --decimal`，同时按 `--help` 提供 mysql、socket、全新 out 目录，密码通过 MYSQL_PWD 环境变量传入。离线回归不需要运行 MySQL。

代码入口为 `schema.go`、`decimal.go`、`record.go`；测试为 `decimal_test.go`，生成脚本负责独立 SQL 预期。test/race/vet 通过，核心包覆盖率 97.0%；10 秒 FuzzDecimal 执行 175177 次，无失败；19 个新文件通过 innochecksum。示例程序的三个表共 608 行与 SQL 预期精确一致。本阶段的完整验证结果见 [任务列表](../TODO.md)。日期时间、浮点、自动元数据、DECIMAL 主键及历史版本仍需后续单独规划。
