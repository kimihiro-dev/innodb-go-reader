# 20 浮点混合记录与验证

## 学习目标

本章用同一记录中的 FLOAT、DOUBLE、DECIMAL、VARCHAR 和页外 TEXT 说明字段定位，再复现 SQL、位模式与示例 JSON 三层对照。前置章节为 19、小数编码 17 和 LOB 解析 13–16。

## 混合记录如何定位

`float_mixed` 包含十个可空浮点列 f0..f9，偶数列为 FLOAT，奇数列为 DOUBLE。id INT 在 SQL 声明中位于 f4 后；另有可空 exact DECIMAL(14,4)、note VARCHAR(32)、body TEXT。物理记录仍以 id 开头，其后为系统字段、f0..f9、exact、note、body。

十三个可空列需要两字节位图：按 f0..f9、exact、note、body 分配 bit 0..12。读位图从靠近记录头的低位字节开始；只有 note/body 消费变长长度项。

真实快照 `float_mixed.ibd.gz` 解压 SHA256 为 `50f6a596ba238a0283a22ef51b5d74e28792d85a6023c5d66c383dc2e483ef99`。下面都是页 4 的十进制页内偏移，End 不包含页外数据：

| id | Start | origin | End | 元信息原始字节（地址递增） |
|---:|---:|---:|---:|---|
| 0 | 120 | 129 | 223 | `00 0a 0000 0000100068` |
| 1 | 223 | 233 | 347 | `14c0 0a 0000 000018007b` |
| 2 | 347 | 356 | 430 | `00 0a 0155 0000200052` |
| 3 | 430 | 438 | 465 | `0a 17ff 000028feba` |

id=0 没有 NULL：note 为“混合😀”，十字节；body 为空串。长度区地址递增为 body 的 00、note 的 0a。值区依次为：

```text
129..133  INT 主键
133..146  13 字节系统字段
146..206  五个 FLOAT 与五个 DOUBLE，合计 5×4+5×8=60 字节
206..213  DECIMAL(14,4)，七字节
213..223  note，十字节
body      空值，零字节
```

加上两字节长度区、两字节 NULL 位图、五字节记录头，本地记录长 `9+17+60+7+10=103`，恰好等于 223−120。区间均为左闭右开。

id=1 的 body 是 60000 字节文本，长度项由 `14 c0` 表示：逆向先读 c0，判定两字节且 external，再读取 14 得到本地引用长度 20。浮点和 DECIMAL 的长度仍来自 schema，不与它混合。完整 body 由已有 LOB 读取还原。

id=2 的逻辑位图是 0x0155，f0、f2、f4、f6、f8 为 NULL。这五个 FLOAT 不占值字节，只留下五个 DOUBLE 的四十字节。id=3 的位图 0x17ff，只有 note 非 NULL；值区只有 17 字节主键/系统字段和十字节 note，不能从空闲空间读取出假的浮点零。

## 四组真实快照

| 夹具 | 行数 | 验证目的 |
|---|---:|---|
| float_values | 143 | 正负零、正规/次正规边界、极值、确定性随机 FLOAT、0.1、2^24 舍入边界、NULL、UNSIGNED |
| double_values | 143 | 相同类别的 DOUBLE 与 2^53 舍入边界 |
| float_mixed | 4 | 跨字节 NULL 位图、主键重排、小数/文本/页外值混合 |
| float_tree | 600 | 乱序插入、root level=1、BIGINT 主键、多页扫描与中文 |

每张 values 表先放 12 个固定位模式，再生成 128 个有限随机位模式，最后补 0.1、整数精度边界和 NULL。生成脚本以 Python struct 将目标位模式转为可往返文本，再经 MySQL CAST 插入；独立预期从已存储列的 SQL JSON 采集。两种来源不能混淆，输入文本不直接当作插入后的真值。

专用库为 `innodb_reader_fixture_21d5d52d4e45`。同一会话执行 FOR EXPORT、SELECT、复制文件和 UNLOCK；未修改既有用户表。交付 4 组快照共 890 行，均通过 SQL 对照；全项目累计 58 组成功夹具、19154 行，加一组已有超限拒绝夹具共 59 组资产。

## 三层验证

1. **SQL 对照**：JSON 预期使用 Go json.Decoder.UseNumber 保留数字文本，按目标列宽度 ParseFloat，再用 Float32bits/Float64bits 比较。NULL 单独判断；其他整数/小数/文本仍遵循原有契约。
2. **原始文件对照**：values 表字段地址从记录 origin 推导，直接读取小端位模式，与公开返回值的位模式比较，包括负零；每份原文件先验证 SHA256。
3. **示例 JSON 往返**：标准 Go JSON 编码后按列宽度解析，再比较位模式。允许 SQL 和 Go 选择不同的可往返十进制显示，不用宽松误差容忍掩盖错误。

单元测试检查四/八字节宽度、输入不变、正负极小值、符号零，以及 NaN、正负 Infinity、负 UNSIGNED 和错误 schema。整表损坏测试修改浮点字节或 unsigned 契约，要求 ErrCorrupt 且结果 nil；不能在错误发生前返回部分行。FuzzFloat 对包含 LOB 的混合夹具进行有界修改，验证读取不崩溃及无部分结果。

当前不在 Read 路径验证 CRC。随机字节变化若仍形成合法有限数，结构检查不一定能发现；错误外部 schema 也可能具有相同宽度，因此这些验证不等于任意损坏检测或自动元数据校验。

## 复现

```sh
go test ./...
go test -race -cover ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzFloat$' -fuzztime=10s -parallel=2
gzip -dc testdata/floating/float_values.ibd.gz > /tmp/float_values.ibd
go run ./examples/read /tmp/float_values.ibd testdata/floating/float_values.json
```

示例 id=140 输出 `[140,0.1,0.1]`；SQL 对照文件可写为 `0.10000000149011612`。两者按 float32 解读后应为同一位模式。id=1 的 signed value 保留负零；消费端是否保留负零取决于自己的数值处理方式：例如 Python json.loads 默认将 -0 读取为整数 0，会丢失符号；独立示例验证先以 Decimal 保留数字标记，再按列宽度转成浮点位模式比较。

重建使用 `scripts/generate_fixtures.py --floating`，按 `--help` 传入 mysql/socket/全新输出目录，密码通过 MYSQL_PWD 环境提供。固定夹具的离线测试无需运行 MySQL。代码入口是 `float.go`、`schema.go`、`record.go`，测试在 `float_test.go`；test/race/vet 通过，核心包覆盖率 97.1%；10 秒 FuzzFloat 共 142200 次执行，无失败。四份新文件通过官方 innochecksum，示例全部 890 行通过独立对照。完整运行证据见 [任务列表](../TODO.md)。

本阶段不包含浮点主键、旧声明语义、日期时间、自动元数据或历史版本；已有格式限制继续生效。
