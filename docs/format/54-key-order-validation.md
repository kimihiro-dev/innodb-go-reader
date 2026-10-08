# 54：PAD SPACE、DESC 与多类型树验收

本章在[完整键解码](53-typed-key-layout.md)的基础上，解释同一份字节怎样决定页内顺序和子树范围。核心入口是 [key.go](../../key.go) 的 `compareIndexKey` / `comparePadded`，整个索引始终使用同一套比较器。

## 1. binary 和 `_bin` 不是同一条规则

BINARY/VARBINARY 使用原始字节字典序。相同前缀下，较短的值在前：空值 < `00` < `20`，`61` < `6100` < `6120`。BINARY 的补零是存储值的一部分，不能删除；长度相同并不意味着可以按 C 字符串遇零停止。

本阶段四种字符 `_bin` 都是 PAD SPACE。它们先比较共同部分，遇到长度差时把缺失字节视作 0x20，再继续比较。

| 比较 | 结果 | 原因 |
|---|---|---|
| `a` 与 `a ` | 相等 | 缺失位置按空格补齐 |
| `a` 与 `a  ` | 相等 | 多个尾空格仍相等 |
| `a` 与 `a\x00` | 前者较大 | 虚拟 0x20 大于真实 0x00 |
| `a` 与 `a\x1f` | 前者较大 | 0x20 大于 0x1f |
| `a` 与 `a!` | 前者较小 | 0x20 小于 0x21 |

因此，“先去掉尾空格再调用普通字节比较”是错误的：它会把 a 排在 `a\x00` 前面。当前实现不分配补齐缓冲区，而是逐位置读取真实字节或虚拟空格；测试的独立 oracle 则实际分配补齐缓冲区后调用标准库 `bytes.Compare`。

MySQL 源码依据是 `strings/ctype-bin.cc:175–209` 的 `my_strnncollsp_8bit_bin` 与 `strings/ctype-mb.cc:440` 附近的 `my_strnncollsp_mb_bin`。ASCII/latin1 在单字节编码上比较；有效 UTF-8 的字节序保持 Unicode 码点序，但必须先校验编码。这里没有移植 UCA 权重表，不能据此扩展到 `_ai_ci`、`_0900_bin` 等规则。

## 2. 为什么不能比较返回给调用方的字符串

MySQL latin1 的 0x80 解码为欧元符号 €，0xff 解码为 ÿ。二者在 latin1_bin 的原编码顺序是 `80 < ff`；如果先转为 UTF-8，再比较其字节，就变成 `e2 82 ac > c3 bf`，顺序相反。

真实 `keys_latin1_varchar_asc` 的 SQL 顺序为：

```text
空字符串, A, a\x00, a\x1f, a[两个空格], a[一个空格], a, a!, b, z, €, é, ÿ
```

其中 a 的三个尾空格变体在第一个成员上相等，由第二成员 `tie DESC` 决定 3、2、1 的顺序。SQL 预期独立保存在 `.expected.json.gz`。这同时验证了 PAD SPACE、控制字节、原编码排序和逐成员方向。

所以 `Values` 用于展示/业务消费，`TextBytes` 保留文本物理字节，内部键副本用于树比较。CHAR 的 `CharStorage` 仍保存物理文本，`Values` 仍去尾 U+0020；这些已有公开约定不因主键而改变。

## 3. DESC 反转比较，不反转载荷

`storage/innobase/rem/rem0cmp.cc:319–448` 按字段类型比较，再应用 `is_asc`。本阶段每个成员得到比较符号后，若该成员 DESC 就取相反符号。不要把所有元组统一反序，因为 `(a DESC,b ASC)` 与 `(a DESC,b DESC)` 不相同；不要对读到的 DESC 字节做按位取反。

例如第 53 章的 tie=2832 虽然是 DESC，物理字节仍是 `80 00 0b 10`。真实 `keys_decimal_desc` 首行是 DECIMAL(65,30) 的最大正值，页 4、origin=317，载荷以 `85 f5 e0 ff 3b 9a c9 ff` 开头，仍符合原正数 DECIMAL 编码。后续是最小正小数、零、负小数、最小负值。

DATE/YEAR 的整数编码、BIT 的大端编码、当前 DATETIME/TIME/TIMESTAMP 及 NEWDECIMAL 的定长编码均按原字节序比较，解码器先验证其合法性。真实 `keys_time6_asc` 的三个起始键分别为：

| 页 4 origin | 键字节 | 解码值 |
|---|---|---|
| 125 | `4b 91 05 fe 4d f9` | -838:59:58.111111 |
| 149 | `7f ff fe fe 4d f9` | -00:00:01.111111 |
| 173 | `80 00 00 00 00 00` | 00:00:00.000000 |

负小数的补码/借位已由原 TIME 解码器处理；不能把显示字符串顺序作为数值顺序。FLOAT/DOUBLE 和 ENUM/SET 需要另行确定比较与元数据契约，仍明确拒绝作为键。

## 4. 所有范围必须使用索引顺序

页内、跨叶子页、非叶子有限键以及父子 `[L,H)` 范围都使用相同的逐成员比较器。这里的“递增”是按索引定义顺序，不是每个成员的 SQL 自然升序。任意一个检查遗漏 DESC 或 PAD SPACE，都可能错拒绝正常树或接受错误子树。

`nil` 仍代表无界；MIN_REC 的存储载荷仍不是有限下界。真实 `keys_deep` 根页第一条 MIN_REC 保存 k 的前缀 "0056"，其子树中却有先于它的 "0059"（k DESC）。如果把哨兵当成普通键，合法行就会落在错误范围外。它必须继承父任务的下界。

公开的 `NodePointer.Key` 单列仍是一个原类型值，多列仍是按键顺序的 `[]any`；内部范围保存原始成员字节。完整元组比较相等才视为重复主键，单个字符成员 PAD SPACE 相等时必须继续比较后续成员。

## 5. 本阶段真实验收矩阵

最终资产位于 `testdata/keys`，独立数据库为 `innodb_reader_keys_bedf314bb4a3`。生成程序只创建新库、修改生成会话的 sql_mode/time_zone；使用 `FLUSH TABLES ... FOR EXPORT` 锁定快照，复制后解锁。没有修改既有用户表、全局配置或停止原实例。

| 组别 | 覆盖 |
|---|---|
| 四个字符规则 × CHAR/VARCHAR × ASC/DESC | 16 个小表及各自 300 行树；空值、空格、控制字节、大小写、重音、中文/emoji（字符集允许时）、latin1 € |
| BINARY / VARBINARY × ASC/DESC | 4 个小表及各自 300 行树；空值、零、空格、0xff |
| DECIMAL、BIT、DATE、YEAR × ASC/DESC | 8 个小表及各自 300 行树；精确大数、uint64 高位、日期/年份零值与端点 |
| 三种时间类型 × 全七种 fsp × ASC/DESC | 42 个小表及各自 300 行树；零值、负时长、极值、小数 |
| lesson / empty / deep / max_width | 非连续 SQL 列序、普通 LOB、空表、3000 行三层树、127/128/255/256 长度边界及 3072 字节完整键 |

共 **144 文件、24433 行**；其中 70 个类型矩阵树共 21000 行，deep 单独 3000 行、494 个聚簇页。144 文件均通过官方 `innochecksum --strict-check=crc32`；288 个 SDI 对象与官方 `ibd2sdi` 逐对象对照。全部 CLI 行与 SQL 完整 ORDER BY 结果一致，二进制经 Base64 解码后对照 SQL HEX，DECIMAL/时间按精确字符串对照，BIT/整数不经过 float64。检查记录在 `verification.json`。

[离线测试](../../key_test.go)还核对每份 SHA256、自动/手工 schema、完整返回结果，并要求每个 `_tree` 真正有非叶子页。损坏测试仅改内存副本并重封 CRC，覆盖重复键、方向错误、叶子/非叶子外部标志、非法长度、零子页、错误范围与非法文本；错误时不得返回部分结果。元数据测试拒绝未支持 collation、前缀、未知方向及异常隐藏列方向。

原 207 份资产保持不变；合计 **351 份资产，348 份成功资产、57832 行，另 3 份既有拒绝资产**。旧测试中仅将本阶段已经支持的主键契约从拒绝改为接受；未知排序方向仍拒绝。

完整 race/vet 通过，核心包覆盖率 93.7%。10 秒预算 fuzz：FuzzKeyOrder 58722 次、FuzzKeyRead 5 次，无失败；文件种子较大，后者仅作为整文件入口冒烟，不能替代上述结构损坏与真实 SQL 验收。

## 6. 复现

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzKeyOrder$' -fuzztime=10s -parallel=2
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzKeyRead$' -fuzztime=10s -parallel=2
python3 scripts/verify_key_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin
```

默认 Go 测试完全离线。重新采集时用 `scripts/generate_key_fixtures.py --mysql ... --socket ... --out 新目录`，密码只通过调用环境 `MYSQL_PWD` 提供；输出目录必须不存在，完整实际 SQL 写入 `generate.sql.gz`。开发过程中曾修正 TIME 非法端点，并补齐多页及 latin1 对照；最终只交付上述完整矩阵，早期独立测试库未删除。

其他 collation、前缀键、隐藏 ROW_ID、二级索引及 UPDATE/DELETE 仍未扩展。下一计划是第 26 阶段：无显式主键时识别唯一非空聚簇索引或隐藏六字节 DB_ROW_ID。
