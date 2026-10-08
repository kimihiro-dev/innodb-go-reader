# 26. TIME：混合记录和正负边界验证

<a id="学习目标"></a>

## 学习目标与前置概念

将不同精度的 TIME 放回记录，验证其与 DATETIME、NULL 位图和页外文本的组合，并区分插入时舍入与物理解码。

## 混合记录布局

time_mixed 的 t0..t8 都是可空 TIME，fsp 为 0、1、2、3、4、5、6、0、1。id INT 在声明中位于 t3 后；后面还有可空 stamp DATETIME(6)、note VARCHAR(32)、body TEXT。物理上主键仍在最前。

十二个可空列需要两字节位图：bit 0..8 对应 TIME，bit 9 为 stamp，bit 10 为 note，bit 11 为 body。只有 note/body 有变长长度项。

`time_mixed.ibd.gz` 解压 SHA256 为 `fc0adad13bc267ba3e585cedaa8e416dd75f1b79fcce1d250706503643c4b17b`。以下均是页 4 的十进制页内偏移：

| id | Start | origin | End | 原始元信息 |
|---:|---:|---:|---:|---|
| 0 | 120 | 129 | 204 | `00 0a 0000 0000100055` |
| 1 | 204 | 214 | 309 | `14c0 0a 0000 0000180068` |
| 2 | 309 | 318 | 371 | `00 0a 0155 000020003d` |
| 3 | 371 | 379 | 406 | `0a 0bff 000028fef5` |

id=0 所有列非 NULL，TIME 值均为负时长。九列宽度为 `3+4+4+5+5+6+6+3+4=40` 字节，note 为“时长😀”，十字节，body 为空串：

```text
129..133  主键
133..146  13 字节系统字段
146..186  九个 TIME
186..194  DATETIME(6)
194..204  note
body      零字节
```

区间均左闭右开。全记录长 `2 长度+2 位图+5 头+17 主键/系统+40 TIME+8 DATETIME+10 文本=84` 字节，与 204−120 相符。

id=1 的 body 为 60000 字节页外文本，元信息含 `14c0`，表示二十字节本地引用；整条本地记录多二十一个字节。已有 LOB 代码还原完整文本，TIME 字段不走页外分支。

id=2 的位图 0x0155 将偶数 TIME 列置 NULL，少读 3+4+5+6+4=22 字节；End−origin 为 53。id=3 的位图 0x0bff 只保留 note，值区剩十七字节主键/系统和十字节文本，不能继续读取 NULL TIME 的固定宽度。

## 九组真实样本

| 夹具 | 行数 | 覆盖 |
|---|---:|---|
| time_fsp_0..6 | 七表各 31 行，共 217 行 | 正负零、整秒、亚秒、25 小时、±838:59:59、临近端点、小数步长/尾数、舍入进位、NULL |
| time_mixed | 4 | 多精度、跨字节位图、主键重排、DATETIME/中文/页外 TEXT |
| time_tree | 600 | 乱序插入、BIGINT 主键、root level=1、正负 TIME(6)、TIME(3) 亚秒值和 NULL |

专用库是 `innodb_reader_fixture_e26673c3973a`，只新增测试数据，不改既有表。生成连接将会话 sql_mode 设为 STRICT_TRANS_TABLES，并写入生成 SQL 和 manifest；不改全局模式。SELECT 和文件复制都在同一连接的 FOR EXPORT 锁内完成。

九组共 821 行新增 SQL 对照成功，全项目累计 80 组成功夹具 22630 行；另有一组既有超限拒绝夹具，总计 81 组资产。SQL 对 TIME 使用 CAST AS CHAR，保留符号与声明小数位，固定原始文件以 SHA256 验证。

## 负时长进位与存储值

每张 fsp 表 id=29/30 分别插入 `00:00:59.999999` 和 `-00:00:59.999999`。fsp=0..5 时，本实例舍入到正负 `00:01:00` 并带目标精度的零尾数；fsp=6 保留原值。

fsp=2 的实际字节为 `80004000` 与 `7fffc000`，而不是按十进制数字逐位进位。测试单独断言这些 SQL 存储结果，不要求解析器撤销舍入。小数最小步长、±端点和正负零也有独立断言，避免仅有行数对照。

## 损坏验证与限制

每个 fsp 都测试小时 839、保留位被置位、分钟/秒 60、正负小数越界、端点带非零小数，以及截断可用堆区域。奇数 fsp 另外测试正负小数低位不对齐。所有异常要求 ErrCorrupt 且 Read 返回 nil，不交付已解析的前几行。

单元测试包含正负黄金字节、七种精度的负亚秒值、输入不变、错误长度、非法 schema 属性与 TIME 主键拒绝。FuzzTime 修改包含 LOB 的混合样本，验证无崩溃和无部分结果。

这里的范围检查不能检测所有字节损坏：变化后仍可能是合法 TIME；Read 也尚未内置 CRC 校验。fsp 1/2 等组合具有相同物理宽度，错误的可信 schema 不一定可识别。旧 TIME 格式、TIMESTAMP、时区、时间类型主键尚未实现。

## 复现

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzTime$' -fuzztime=10s -parallel=2
gzip -dc testdata/times/time_fsp_6.ibd.gz > /tmp/time_fsp_6.ibd
go run ./examples/read /tmp/time_fsp_6.ibd testdata/times/time_fsp_6.json
```

id=14 输出 `[14,"-00:00:00.000001"]`；id=21 输出 `[21,"-12:34:56.123456"]`。NULL 是 `[0,null]`，而零是 `[1,"00:00:00.000000"]`。

重建使用 `scripts/generate_fixtures.py --times`，按 `--help` 提供 mysql/socket/全新输出目录，密码通过 MYSQL_PWD 环境变量传入。离线回归无需数据库。代码在 `time.go`、`schema.go`、`record.go`，测试在 `time_test.go`；test/race/vet 通过，核心包覆盖率 97.6%；10 秒 FuzzTime 执行 186673 次，无失败。九个文件通过官方 innochecksum，示例全部 821 行与 SQL 精确一致。完整验证记录见 [任务列表](../TODO.md)。
