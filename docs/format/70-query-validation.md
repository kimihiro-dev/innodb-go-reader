# 70. 查询键编码与独立验收

本章目标：区分用户输入值、物理键字节和 SQL 比较语义；用真实快照证明查询结果正确，并证明它确实跳过无关子树。导航算法和报告契约见[第69章](69-key-query-navigation.md)。

<a id="一输入键必须可精确解释"></a>

## 输入键必须可精确解释

`query_key.go` 将查询值编码为既有 `key.go` 比较器使用的物理键，不另造一套 SQL 隐式转换。查询开始时独立复制归一化后的键，回调修改调用方原键不改变进行中的选择。

| 键类型 | Go输入 | 拒绝例子/边界 |
|---|---|---|
| 各宽度整数、YEAR、BIT | Go有符号/无符号整数或精确json.Number | float64、越界、负数转unsigned；YEAR仅0或1901..2155 |
| 隐藏ROW_ID | 一个48位无符号范围内整数 | 2^48、负数、多个成员 |
| BINARY/VARBINARY | `[]byte` | BINARY必须精确声明宽度，VARBINARY不可超宽 |
| CHAR/VARCHAR | UTF-8 Go字符串 | 无效UTF-8、不可编码字符、超字符数 |
| DECIMAL | 规范十进制字符串，固定scale | 指数、超precision、多余前导零、负零 |
| DATE/DATETIME/TIME/TIMESTAMP | 与Read一致的规范字符串 | 精度不符、尾随内容、超范围、非规范零值 |

整数不能经 JSON float64 中转，否则 uint64 上端及超过2^53的键可能失真。带符号整数恢复原有符号位翻转，DECIMAL使用九位分组与符号变换，TIME负小数处理整数借位。时间编码后调用已有解码器核对规范往返，避免另一套边界定义。TIMESTAMP固定UTC；DATE/DATETIME保留已支持的零分量或无效日历组合，不借用time.Time归一化。具体支持范围仍由schema验证决定，不能因输入编码器接受某种值就扩展键类型。

字符输入统一UTF-8，但比较使用列的实际编码。MySQL latin1的欧元符号映射为单字节0x80，不能按UTF-8的E2 82 AC比较；ascii和utf8mb3也必须检查编码范围。方向由既有比较器逐成员应用，不能预先把查询键反码后再次应用DESC。CHAR显示值会去除尾空格，键比较仍按已验收_bin规则的PAD SPACE语义。

<a id="二三份真实快照与92条独立sql"></a>

## 三份真实快照与92条独立SQL

`testdata/query/manifest.json` 保存原始解压文件SHA、行数、版本和环境；`*.expected.json.gz` 是SQL整表预期，`*.queries.json.gz` 同时保存请求、独立SQL及结果。不是把Go全表结果过滤后当作唯一基准。

| 表 | 当前行 | 聚簇页/根level | 查询数 | 覆盖 |
|---|---:|---|---:|---|
| signed_rows | 1801 | 131/1 | 30 | BIGINT有符号两端、负数、缺失键0、稀疏间隔、长TEXT |
| mixed_rows | 800 | 74/1 | 40 | COMPACT、tenant ASC/code DESC/seq ASC、latin1_bin前缀 |
| unsigned_rows | 5 | 1/0 | 22 | 0、1、2^63−1、2^63、2^64−1及NULL普通列 |

合计2606快照行、92条查询的3939行有序结果。signed_rows包含一个24KB的页外文本。mixed_rows包含空串、`a`、`a `、带NUL的`a`、`b`、欧元符号；尾空格相等的前缀必须一起命中。

对于 `(tenant ASC, code DESC, seq ASC)`，下界 `(t,c,s)` 的闭谓词按字典序展开：

```sql
tenant > t OR
(tenant = t AND code < c) OR
(tenant = t AND code = c AND seq >= s)
```

上界相反，开边界的末项改用严格比较，Reverse反转ORDER BY各成员方向，Limit放在排序之后。前缀用对应完整前导列的等值谓词。脚本不假设混合DESC可以直接使用自然升序行构造器比较。

第一次对照发现一项基准陷阱：仅写 `CONVERT(X'...' USING utf8mb4)` 会让比较发生字符集/排序规则隐式转换，失去目标latin1_bin的PAD SPACE语义。现在文字常量明确写为：

```sql
CONVERT(CONVERT(X'E282AC' USING utf8mb4) USING latin1) COLLATE latin1_bin
```

40条mixed查询经只读SQL重新采集，所有原始ibd及SHA保持不变。旧查询结果保留在 `coercion-probe.json.gz` 作为诊断记录，不作为正确性基准；writer.sql.gz保留原采集和修正后的SQL。生成器已修正，重新采集不依赖手工修补。

快照来自隔离临时MySQL8.0.45实例的新库 `innodb_reader_query_801d543ea403`，写入提交后通过FOR EXPORT复制。临时实例已正常关闭；没有改动原实例、既有用户表或全局配置。官方innochecksum严格CRC32、ibd2sdi、Go SDI/物化/查询CLI均通过，工具路径及逐资产结果见verification.json；physical.json为Go派生结构统计，不冒充独立SQL证据。

<a id="三现有资产回归与错误验证"></a>

## 现有资产回归与错误验证

全部472份资产中470份成功恢复90082行，2份既有类型/布局拒绝继续保留。查询矩阵对成功资产执行无界、反向、1386次点查和686次范围/limit对照；482次完整前导列前缀查询覆盖不同长度、反向和PAD SPACE。已有COMPACT、INSTANT、STORED/VIRTUAL、隐藏ROW_ID、更新/delete-mark及各键类型均复用原始快照。

`TestQueryNavigation` 使用12000行三层树，点查3页对照完整读取1718页；25行跨叶范围正反向均6页。损坏注入只改内存副本：未访问叶页损坏时点查仍成功而Read失败；所选叶页损坏必须拒绝。该对照同时证明查询没有先全表扫描后过滤。

`TestQueryNoUnmatchedLOB` 在既有16777217字节超限资产上查询缺失键2，成功零行且不跟随范围外LOB；查询实际键0仍报ErrUnsupported，保留完整类型物化上限。控制测试覆盖主动停止、上下文取消、页/行预算、自定义回调错误与中途I/O失败；计数、Complete和已交付前缀均独立断言。输入测试覆盖非法成员数、Prefix冲突、整数64位端点、精确小数与时间、全部256个MySQL latin1字节，以及回调修改输入键后的稳定性。

本次全量race/coverage通过，核心覆盖率94.4%，vet通过。10秒预算FuzzQueryRanges执行55923次，以独立整数谓词计算范围/Reverse/Limit预期；FuzzQueryKeys执行1943449次，验证被接受的规范字符串与已有解码器往返一致。fuzz执行次数与机器和语料有关，不是性能基准。

<a id="四复跑与cli"></a>

## 复跑与CLI

在仓库根目录执行：

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go test -run '^TestQuery' -v .
go test -run '^$' -fuzz '^FuzzQueryRanges$' -fuzztime=10s .
go test -run '^$' -fuzz '^FuzzQueryKeys$' -fuzztime=10s .
python3 scripts/verify_query_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin
```

普通Go测试和官方工具复核均使用已保存资产，不需运行MySQL。重新生成使用 `scripts/generate_query_fixtures.py --help` 查看连接/新输出目录参数；生成会创建新库，属于独立采集流程，不是离线测试步骤。

`examples/query` 接受未压缩ibd和一个JSON请求文件，例如：

```json
{"Lower":{"Key":[5988],"Inclusive":true},"Upper":{"Key":[6012],"Inclusive":true},"Reverse":true,"Limit":10}
```

```sh
go run ./examples/query /path/to/deep_rows.ibd /path/to/query.json
```

CLI使用UseNumber保留大整数，并拒绝未知字段/多个JSON对象。二进制成员使用 `{"base64":"AP8="}` 包装，而普通字符串始终是文本；例如 `{"Prefix":[{"base64":"AP8="}]}`。含VIRTUAL表需显式 `-materialized`。

输出逐行event及最后report。必须同时检查退出状态、error和Complete，不能只看已经收到多少行；达到查询Limit是成功，预算ErrLimit是失败。用户回调和底层ReaderAt的阻塞仍不保证能被context强制打断，文件生命周期归调用方。

第34阶段二级记录解析尚未实现；本阶段不提供SQL表达式执行、LIKE、任意collation、回表或MVCC快照恢复。
