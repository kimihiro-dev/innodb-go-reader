# 22. 日期混合记录、特殊值与验证

<a id="学习目标"></a>

## 学习目标与前置概念

将 DATE/YEAR 放回完整记录，理解跨字节 NULL 位图、主键重排、变长文本和页外引用如何共同决定偏移，并复现“SQL 特殊日期确实原样存储”的证据。

## 混合记录布局

date_mixed 有十个可空列 d0..d9：偶数列 DATE、奇数列 YEAR。id INT 主键在声明中位于 d4 后，随后还有可空 note VARCHAR(32)、body TEXT。物理上 id 仍位于 origin，接着是 13 字节系统字段，再排其他列。

十二个可空列占两字节位图，bit 0..9 对应 d0..d9，bit 10 为 note，bit 11 为 body。DATE/YEAR 都是定长列，不占变长长度项；只有 note/body 消费长度元数据。

`date_mixed.ibd.gz` 解压 SHA256 为 `b6e3e72f759cce75c54a84872db66e516d2904b3d6071386ed2bc5dea93ea53d`。以下均为真实页 4 的页内偏移：

| id | Start | origin | End | 元信息（地址递增） |
|---:|---:|---:|---:|---|
| 0 | 120 | 129 | 176 | `00 0a 0000 0000100039` |
| 1 | 176 | 186 | 253 | `14c0 0a 0000 000018004c` |
| 2 | 253 | 262 | 294 | `00 0a 0155 0000200028` |
| 3 | 294 | 302 | 329 | `0a 0bff 000028ff42` |

id=0 没有 NULL。note 为“日期😀”，十字节；body 为空串。长度区从靠近位图的方向读，先得到 note 的 0a，再得到 body 的 00。值区如下，区间为左闭右开：

```text
129..133  INT 主键
133..146  DB_TRX_ID + DB_ROLL_PTR
146..166  五个 DATE 与五个 YEAR，5×3+5×1=20 字节
166..176  note，十字节
body      空串，零字节
```

本地记录长 `2 长度 + 2 位图 + 5 头 + 17 主键/系统 + 20 日期/年份 + 10 文本 = 56` 字节，与 176−120 一致。

id=1 的 body 是 60000 字节“界”文本，长度区出现 `14 c0`：逆向读 c0 判定 external，再读 14 得到 20 字节本地引用。记录本地多一个长度字节和二十字节引用，共 77 字节；已有 LOB 解析器负责恢复完整 body，Record.End 仍不含页外字节。

id=2 的逻辑位图为 0x0155，五个 DATE 为 NULL，不消耗十五个日期字节。五个 YEAR 仍占五字节，End−origin 为 32。id=3 的逻辑位图 0x0bff 表示 d0..d9 和 body 为 NULL，仅 note 保留，因此值区只剩 17+10=27 字节。位图的低位字节离记录头更近，不能把显示的 `0b ff` 直接按读记录头的方式使用。

## 四组一致快照

| 夹具 | 行数 | 验证范围 |
|---|---:|---|
| date_values | 14 | NULL、全零/部分零、年 0/1/999/1000/9999、闰日及非日历日期 |
| year_values | 257 | 全部 256 个 YEAR 存储字节，加 NULL |
| date_components | 1664 | 年 1900/2000/2024/9999 × 月 0..12 × 日 0..31，乱序插入、root level=1 |
| date_mixed | 4 | 两字节位图、主键在声明中间、空文本/中文及页外 TEXT |

date_components 不只覆盖正常日历日期：四个年份各测试 13×32 种分量，每种都要求 SQL 预期和解析结果等于指定格式字符串。date_values 也与固定输入列表核对，避免“服务器把特殊值转换为零后，解析器恰好也输出零”造成假通过。

测试库为 `innodb_reader_fixture_c26bba055b94`。生成 SQL 首条设置 `SET SESSION sql_mode='ALLOW_INVALID_DATES'`，manifest.environment.sql_mode 记录实际会话模式。会话结束后该设置消失，不修改全局模式。只新增唯一专用库，未修改既有用户表。

同一连接持有 FOR EXPORT 锁，在释放锁前执行 SELECT 并复制文件。DATE SQL 预期使用 CAST AS CHAR，YEAR 保持数值，NULL 独立；所有原始文件校验 SHA256。交付四组新样本 1939 行，全项目累计 62 组成功夹具 21093 行，另有一组既有超限拒绝样本，总资产 63 组。

## 结构检查不等于日历检查

损坏测试在内存副本中写入错误日期符号、年 10000、月 13，或缩短记录可用堆区域；要求 ErrCorrupt 且结果 nil。独立解码测试覆盖缺字节/多字节、关键原始编码和输入不变。schema 拒绝 unsigned、长度/精度属性以及 DATE/YEAR 主键。

日字段只有五位，无法单独编码日 32：修改到这个数会影响月份，未必产生结构上非法的结果。YEAR 的全部一字节值均合法。这些类型中的任意改字节不保证能被语义校验检出，Read 也尚未内置 CRC 验证。

日期 1900-02-29 在日历上不成立，但可在本样本的 SQL 模式下保存，应忠实返回，不能归类为文件损坏。相反，月 13 超出当前物理分量契约，必须拒绝。离线解析不依赖正在运行的实例或当前会话模式；错误外部 schema 仍不保证可检测。

## 复现与实现入口

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzDate$' -fuzztime=10s -parallel=2
gzip -dc testdata/dates/date_values.ibd.gz > /tmp/date_values.ibd
go run ./examples/read /tmp/date_values.ibd testdata/dates/date_values.json
```

前两行分别是 `[0,null]` 与 `[1,"0000-00-00"]`。year_values 中 id=0 对应数值 0，id=124 对应 2024，id=256 对应 null。示例对照无需日期库或时区配置。

重建使用 `scripts/generate_fixtures.py --dates`，按 `--help` 传入 mysql/socket/全新输出目录，密码通过 MYSQL_PWD 环境变量提供。交付的离线回归不需要数据库连接。

入口为 `schema.go`、`date.go`、`record.go`；测试在 `date_test.go`，生成器负责 SQL 和物理快照。test/race/vet 通过，核心包覆盖率 97.3%；10 秒 FuzzDate 执行 180503 次，无失败。四份新文件通过官方 innochecksum；示例全部 1939 行与 SQL 预期一致。完整验证证据见 [任务列表](../TODO.md)。后续 DATETIME/TIME/TIMESTAMP 需要另行处理时间分量、小数秒、负时长或时区，本阶段不声称支持。
