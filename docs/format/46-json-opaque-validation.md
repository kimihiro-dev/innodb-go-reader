# 46 JSON 扩展值、页外读取与验证

本章承接[二进制JSON](45-binary-json.md)，解释普通JSON文本表达不了的类型，以及怎样分别验证“显示正确”和“类型正确”。

## Opaque不是忽略字段的理由

opaque的外层标签为0f；之后是1字节MySQL字段类型、变长载荷长度、完整载荷。已知格式解码，同时保留原类型号与字节；未知扩展保留完整载荷，不跳过或填NULL。

官方8.0.45源码（根路径见上一章）：

- `sql-common/json_dom.cc:1164–1210`：DECIMAL前缀和解码。
- `sql/my_decimal.h:57–69`：内部decimal的9组容量。
- `sql-common/json_dom.cc:1236–1246`：时间值按8字节小端packed存放。
- `mysys/my_time.cc:1689–1717,1858–1934,2826–2842`：时间字段打包/拆分。
- `sql-common/json_dom.cc:1501–1529,1627–1667`：时间文本、opaque Base64和VAR_STRING特例。

### 真实DECIMAL：不要使用外层SQL声明精度

`json_opaque`的id=1在第4页record origin=128，JSON字段起点145（文件65681）：

```text
0f | f6 | 13 | 25 0a | 80 00 00 0c 14 9a a4 35 0d fb 38 d2 07 5b cd 15 00
opaque 246 长度19 p37 s10                  decimal主体17字节
```

SQL使用 `CAST(... AS DECIMAL(40,10))` 生成值，但JSON载荷声明precision=37、scale=10；必须使用载荷自己的37，而不是SQL中的40。MySQL内部decimal按十进制大组保存，类型声明与内部有效存储参数并非同一个概念。

整数位37−10=27，占3个4字节大端组。去掉首字节符号翻转后，三个整数组为12、345678901、234567890；小数的9位组为123456789，最后1位为0。完整值为：

```text
12345678901234567890.1234567890
```

DECIMAL主体仍使用[第17章](17-decimal-encoding.md)的大端分组规则；不要因为JSON容器小端，就把decimal主体也反转。Opaque.Data保留前缀和主体，Decoded保留固定小数位。普通JSON视图输出精确数字文本，不通过float64转换。

内部JSON decimal使用my_decimal的9个大组容量，上限可达81位；整数、小数分别向上取整占组，总和不得超过9。普通表列的DECIMAL(65,30)限制保持不变。实际夹具验证两种DECIMAL；precision81的内部容量边界另有明确合成测试，不冒充真实SQL列声明。

### 日期时间：另一种packed格式

JSON时间payload是有符号8字节小端数。DATE/DATETIME/TIMESTAMP的非负packed形式为：

```text
ymd = ((year×13 + month) << 5) | day
hms = (hour << 12) | (minute << 6) | second
packed = (((ymd << 17) | hms) << 24) | microseconds
```

DATE的hms和微秒必须为零。TIME只用hms和微秒，负数是对完整packed整数取负；读取负TIME先取绝对值，再拆字段。它不同于普通TIME2列的小数借位编码。

真实json_opaque的id=2包含DECIMAL、DATE、TIME和DATETIME四个子值。JSON payload基准是第4页203。值目录给出的payload偏移分别为16、26、36、46，其中时间子值是：

| payload偏移 | 原始子值载荷（type来自父目录） | 解释 |
|---:|---|---|
| 26 | 0a 08 00 00 00 00 00 ba b2 19 | field type10，DATE 2024-02-29 |
| 36 | 0b 08 c0 1d fe 7c 6f fe ff ff | field type11，TIME -25:02:03.123456 |
| 46 | 0c 08 40 e2 01 b8 c8 ba b2 19 | field type12，DATETIME 2024-02-29 12:34:56.123456 |

例如DATETIME payload中微秒为0x01e240=123456；剩余位按ymd/hms拆解。普通视图按MySQL形式输出六位小数时间，DATE无小数。JSON TIMESTAMP标签7使用相同日期时间packed结构，不能当作Unix秒或再次套用普通TIMESTAMP列的UTC转换；此标签本轮用合成payload核对，本批真实时间节点是DATE/TIME/DATETIME。

解析验证微秒、年/月/日分量、时分秒和TIME端点，不用time.Time归一化部分零日期。所保留的是JSON创建时已打包的字段分量，不能由此恢复原会话时区。

### 其他opaque及显示特例

真实id=3的二进制值保留field type15，SQL JSON_TYPE返回BLOB；外层JSON标签和内部字段号都可能是0f，但含义不同。普通视图使用MySQL的 `base64:type15:...` 字符串形式，并匹配Base64每76字符换行的显示规则。夹具包括空二进制、00ff字节及100字节长值，验证换行不会丢失。

field type253（VAR_STRING）是官方特殊分支，普通视图按UTF-8字符串显示，类型树仍保留opaque身份；本轮使用合成输入验证。其他未知编号保留类型号与原始Data，并生成base64:type视图，不宣称已经理解其SQL语义。

## 真实页外大容器

`json_large`的id=1为大数组，第4页origin=129，本地引用在 `[146,166)`：

```text
00 00 00 c6 | 00 00 00 05 | 00 00 00 01 | 00 00 00 00 00 01 02 05
 space198       first5        version1            length66053
```

完整JSON由5个LOB数据块组成：页5偏移696长15680，页6/7/8各偏移49长16327，页9偏移49长1392。总长度：

```text
15680 + 3×16327 + 1392 = 66053
```

重组后头部为：

```text
03 | 06 00 00 00 | 04 02 01 00
大数组  count6      payload size66052
```

payload头8字节，6个值目录项各5字节，因此数据起点为38（0x26）。第一个字符串长度字段 `e8 81 02`：104 + 1×128 + 2×16384 = 33000。整个文档实际66053字节，是payload size再加根标签1字节。

同表id=2是真实大对象（66040字节），id=3为22000个inline整数的大数组（110009字节）。这些容器跨LOB块，不要求一个值或字符串恰好落在同一物理页；必须先按LOB索引拼成完整字段再解析JSON内部相对偏移。

## 验证矩阵与证据

资产在[testdata/json](../../testdata/json/manifest.json)，专用库 `innodb_reader_json_2bf7dabe3618`。生成器[scripts/generate_json_fixtures.py](../../scripts/generate_json_fixtures.py)新建独立库、只插入，使用同一会话FOR EXPORT锁持有期间复制文件；会话sql_mode=STRICT_TRANS_TABLES、time_zone=+00:00，未修改全局配置或原有表。

| 夹具 | 行数 | 主要覆盖 |
|---|---:|---|
| json_lesson | 23 | SQL NULL/JSON null、空值、UTF-8、各整数范围、double、对象/数组 |
| json_opaque | 3 | DECIMAL精度、日期时间、二进制opaque及Base64换行 |
| json_boundaries | 7 | 127/128、255/256、16383/16384字节字符串及98层数组 |
| json_large | 3 | 三个大容器、页外JSON、22000元素数组 |
| json_tree | 600 | 跨页聚簇树、混合JSON/普通列、嵌套对象 |

合计636行，5个JSON字段页外；3个大容器、2个DECIMAL、3个时间节点和3个二进制opaque。每行JSON后都有普通VARCHAR列，独立核对后续字段，避免“JSON解析正确但记录偏移错位”。

[json_test.go](../../json_test.go)逐行比较JSON_STORAGE_SIZE、JSON_DEPTH、根JSON_TYPE和SQL显示，数字通过精确有理数比较而不是float64；自动schema与显式schema及完整结果一致。json_opaque.types.json保存子节点SQL JSON_TYPE，补充验证DECIMAL/DATE/TIME/DATETIME/BLOB身份；本次是在新库未再变更数据后以只读查询补采，生成器后续重跑会在导出会话内采集。

[verification.json](../../testdata/json/verification.json)记录官方工具版本、严格crc32结果和CLI对照。5文件全部通过innochecksum，10个SDI对象与官方工具一致；普通视图示例636行和类型树示例636行均对照通过。不能把普通JSON视图中两个null相同，误当SQL NULL与JSON null在类型树中也相同。

合成测试另覆盖uint16/uint32、int64/uint64极值、double负零、TIMESTAMP、内部precision81及VAR_STRING特例、100层/100000节点/16MiB边界。损坏测试覆盖非法标签、截断、长度溢出、非法UTF-8、非有限double、重复键、重叠偏移、非法时间及decimal；空洞明确拒绝。页内和页外JSON内容损坏后重新封装CRC，确认能到达JSON校验且ReadAuto不返回部分行。

本阶段完整race/vet通过，核心包覆盖率92.8%；FuzzJSON在10秒预算执行849857次，无失败输入。

## 使用与复现

```sh
# 解压属于调用层，不是InnoDB解析器的一部分
gzip -dc testdata/json/json_lesson.ibd.gz > /tmp/json_lesson.ibd

# 类型树输出，保留SQL NULL/JSON null区别
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/auto /tmp/json_lesson.ibd

# 普通JSON视图；扩展类型信息请继续查看类型树
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/json /tmp/json_lesson.ibd

GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzJSON$' -fuzztime=10s -parallel=2
```

生成器要求不存在的输出目录，密码仅通过MYSQL_PWD传入：

```sh
python3 scripts/generate_json_fixtures.py \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --out /tmp/new-json-fixtures
```

重新生成时索引/表空间ID和哈希会改变；不能将本章的198、5、66053当作格式常量。旧JSON局部更新空洞、事务历史、二级/多值索引和通用JSON查询语言均未在本阶段实现。
