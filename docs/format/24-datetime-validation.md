# 24. DATETIME 混合记录与精度验证

<a id="学习目标"></a>

## 学习目标与前置概念

理解不同 fsp 字段如何共同影响记录偏移，区分 SQL 插入时舍入与离线解码，并复现七种精度、NULL、特殊日期和跨页树的完整对照。

## 混合记录的实际布局

datetime_mixed 有九个可空 DATETIME 列 d0..d8，fsp 依次为 0、1、2、3、4、5、6、0、1；id INT 主键在声明中位于 d3 后。随后还有可空 DATE day、YEAR year、VARCHAR note 和 TEXT body。

十三个可空列占两字节 NULL 位图。d0..d8 对应 bit 0..8，day 为 bit 9，year 为 bit 10，note 为 bit 11，body 为 bit 12；只有 note/body 需要变长长度项。

`datetime_mixed.ibd.gz` 解压 SHA256 为 `14f1fecfe636b528498c70a7a5e32a0ae923d166feeed7b56baf3882472362e5`。真实记录全部在页 4，偏移为十进制页内值：

| id | Start | origin | End | 元信息原始字节 |
|---:|---:|---:|---:|---|
| 0 | 120 | 129 | 218 | `00 0a 0000 0000100063` |
| 1 | 218 | 228 | 337 | `14c0 0a 0000 0000180076` |
| 2 | 337 | 346 | 403 | `00 0a 0155 0000200041` |
| 3 | 403 | 411 | 438 | `0a 17ff 000028fed5` |

id=0 没有 NULL。九个 DATETIME 的宽度为 `5+6+6+7+7+8+8+5+6=58` 字节。值区如下，区间左闭右开：

```text
129..133  主键
133..146  十三字节系统字段
146..204  九个 DATETIME
204..207  DATE
207..208  YEAR
208..218  note “时间😀”，十字节
body      空串，不占值字节
```

加上两字节长度区、两字节位图和五字节记录头，记录长 `9+17+58+3+1+10=98` 字节，与 End−Start 一致。

id=1 的 body 为 60000 字节页外文本，长度元数据多一个字节、本地多二十字节引用，记录长 119 字节。它由现有 LOB 解码拼接，不影响 DATETIME 自身的定长规则。

id=2 的位图 0x0155 将 d0、d2、d4、d6、d8 置 NULL，去掉 5+6+7+8+6=32 字节，值区剩 57 字节。id=3 的位图 0x17ff 只留下 note，值区只有主键/系统字段十七字节加文本十字节。不能为 NULL 的 DATETIME 继续消耗 fsp 指定的宽度。

## 九组真实快照

| 夹具 | 行数 | 覆盖 |
|---|---:|---|
| datetime_fsp_0..6 | 七表，每表 16 行，共 112 行 | 全部精度、NULL、零/部分零/非日历日期、最小小数步长、最大尾数、尾零、整体最大值及跨日舍入 |
| datetime_mixed | 4 | 多精度、两字节位图、主键重排、DATE/YEAR/中文和页外 TEXT |
| datetime_tree | 600 | 乱序插入、root level=1、BIGINT 主键、fsp=6/3 与 NULL、小数秒不同值 |

SQL 预期对 DATETIME 使用 CAST AS CHAR，保留声明小数位。测试将 Read 的行编码为 JSON 后逐列精确比较，同时要求返回 string、输出长度与 fsp 一致，并检查单列样本的实际记录宽度。

专用库为 `innodb_reader_fixture_625d4a88697d`，只新增测试表，没有修改既有用户表。生成连接设置会话级 ALLOW_INVALID_DATES，实际模式记录于 manifest；全局配置不变。快照与预期在同一会话的 FOR EXPORT 锁内完成，全部文件有 SHA256。

本阶段新增九组成功样本 716 行；全项目累计 71 组成功夹具 21809 行，另有一组既有超限拒绝资产，总计 72 组物理样本。

## 存储前舍入不是解码器要撤销的操作

每张 fsp 表的 id=14 都插入同一文本：`2024-02-29 23:59:59.999999`。

- fsp=0..5：本实例将小数秒舍入，进位到 `2024-03-01 00:00:00`，并保留目标精度的零尾数。主体字节为 `99b2c20000`。
- fsp=6：保留原值，主体字节为 `99b2bb7efb`，微秒为 `0f423f`。

测试不仅比较 SQL 输出，还对这一已知边界单独断言，确保验证的是存储后的真实值。解析器不做反向舍入，也不在这里重新应用读取时的 sql_mode。

同理，1900-02-29、零月和零日的样本被单独断言未转换为零日期或规范日历日期。DATETIME 保留字段分量，不能无条件用日期库解析后再格式化。

## 损坏和元数据边界

测试在每个 fsp 的文件副本中修改：负主体编码、年份 10000、小时 24、分钟/秒 60、小数秒字节越界、奇数 fsp 的低位不对齐、记录堆边界截断。要求 ErrCorrupt 且 Read 返回 nil；不允许已经解出的前几行作为部分结果返回。

schema 测试覆盖 fsp=-1/7、错误 unsigned/长度/DECIMAL 属性、其他类型携带非零 fsp，以及 DATETIME 主键拒绝。黄金字节测试覆盖七种小数宽度及读取不修改输入，FuzzDatetime 使用包含 LOB 的混合样本做有界字节变异。

fsp=1 与 2 等组合有相同字节宽度，错误外部 schema 有时仍能得到形式合法的值。当前仍依赖可信元数据，不能保证发现所有 fsp 不匹配，也不自动识别旧 DATETIME 物理格式。主键、表空间及 CRC 等边界沿用已有阶段。

## 复现

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzDatetime$' -fuzztime=10s -parallel=2
gzip -dc testdata/datetimes/datetime_fsp_6.ibd.gz > /tmp/datetime_fsp_6.ibd
go run ./examples/read /tmp/datetime_fsp_6.ibd testdata/datetimes/datetime_fsp_6.json
```

id=12 输出 `[12,"2024-02-29 12:34:56.123456"]`。NULL 行输出 `[0,null]`，零日期时间行输出 `[1,"0000-00-00 00:00:00.000000"]`。

重建使用 `scripts/generate_fixtures.py --datetimes`，按 `--help` 提供 mysql/socket/全新输出目录，通过 MYSQL_PWD 环境传入密码。离线测试不需要 MySQL 连接。

代码在 `datetime.go`、`schema.go`、`record.go`，测试在 `datetime_test.go`。test/race/vet 通过，核心包覆盖率 97.4%；10 秒 FuzzDatetime 执行 170514 次，无失败。九个新文件通过官方 innochecksum；示例全部 716 行与 SQL 精确一致。完整验证结果见 [任务列表](../TODO.md)。TIME 的负时长与 TIMESTAMP 的时间点/时区含义将单独规划，本阶段不处理。
