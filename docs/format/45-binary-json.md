# 45 JSON 列：二进制容器与无损类型树

本章的目标是理解为什么JSON列不能当作UTF-8文本直接读取，以及如何从容器目录找到全部存储值。前置知识是[变长字段](11-variable-lengths.md)、[页外引用](13-lob-reference-and-pages.md)和[自动schema](43-sdi-schema.md)。JSON列仍使用InnoDB变长记录和LOB；本章解释取出完整字段字节后的内部格式。

## 三层结构与三种偏移

```text
聚簇记录的NULL位图、变长长度数组
                 ↓
       页内字段或20字节LOB引用
                 ↓ 按LOB索引拼接完整字段
     [JSON类型标签1字节][根值payload]
                 ↓
      容器头 → 键目录/值目录 → 子值
                 ↓
       JSONValue类型树 → JSON()普通视图
```

InnoDB页头和LOB引用中的多字节整数通常是大端；本章JSON内部的长度、偏移、整数、double却是**小端**。不要共享一个全局“所有整数同字节序”的假设。

固定区分：文件绝对偏移、页内偏移、JSON容器payload相对偏移。最后一种从该容器的count字段开始，**不包含外面的类型标签**。嵌套容器有自己的基准；不是永远相对于整份JSON的起点。

官方依据为用户提供的MySQL8.0.45源码根 `/Users/kimihiro/workspace/codespace/cpp/mysql-8.0.45`：

- `sql-common/json_binary.h:30–157`：完整格式语法。
- `sql-common/json_binary.cc:285–312`：变长长度；398–411：inline类型。
- `sql-common/json_binary.cc:920–1065`：标量/容器解释；1090–1163：元素与键偏移。
- `storage/innobase/handler/ha_innodb.cc:8009`：JSON字段按BLOB存储。

Go标准库encoding/json解释文本JSON，不能解码此格式。本实现用encoding/binary读取存储结构，只在普通视图输出和SQL预期比较时使用encoding/json。

## 类型标签

| 标签 | 存储值 | payload |
|---|---|---|
| 00 / 01 | 小/大对象 | count、size、键目录、值目录、键和值 |
| 02 / 03 | 小/大数组 | count、size、值目录、值 |
| 04 | literal | 00=null，01=true，02=false |
| 05 / 06 | int16 / uint16 | 2字节小端 |
| 07 / 08 | int32 / uint32 | 4字节小端 |
| 09 / 0a | int64 / uint64 | 8字节小端 |
| 0b | double | 8字节小端IEEE754 |
| 0c | 字符串 | 变长字节长度、UTF-8载荷 |
| 0f | opaque扩展 | MySQL字段类型1字节、变长长度、载荷 |

这里的0f是JSON外层类型，opaque内部又有一套MYSQL_TYPE编号；它也不同于SDI的DD type=31。解析器分别处理这些编号，不能互相替换。

符号整数没有普通InnoDB整数列的高位翻转；例如-32768直接为 `05 00 80`。int64/uint64直接返回Go对应类型，不通过float64转换。浮点保留实际float64位模式和负零；非有限值报错。

字符串长度按7位一组组成，小端组序，每字节最高位表示是否继续，最多5字节且不超过uint32。它计**字节数**，不是字符数。JSON字符串内的零字节/换行可直接存入载荷，普通视图输出时再转义。

## 真实标量字节

以下来自 `testdata/json/json_lesson.ibd.gz` 解压后的第4页，SQL及独立预期一起交付。记录中id为4字节，随后DB_TRX_ID为6、DB_ROLL_PTR为7，所以本表非NULL doc的本地数据起点为 `record.Offset+17`；这只是当前表布局，不是JSON格式常量。

| id | 页内doc起点 | 文件偏移 | 原始字节 | 解码 |
|---|---:|---:|---|---|
| 2 | 179 | 65715 | 04 00 | JSON null |
| 3 | 216 | 65752 | 04 01 | true |
| 4 | 253 | 65789 | 04 02 | false |
| 5 | 290 | 65826 | 00 00 00 04 00 | 空对象 |
| 6 | 330 | 65866 | 02 00 00 04 00 | 空数组 |
| 7 | 370 | 65906 | 0c 00 | 空字符串 |
| 10 | 496 | 66032 | 05 00 80 | int16的-32768 |

id=1是SQL NULL，由记录NULL位图识别，根本没有JSON类型标签。它不能和id=2的两个真实字段字节合并。

id=8在页内407（文件65943）：

```text
0c | 0c | e4 b8 ad e6 96 87 f0 9f 98 80 00 0a
字符串 12字节        中文          😀         零 换行
```

载荷是12字节，解码后的普通视图为 `"中文😀\u0000\n"`（具体转义大小写/空白不是数据差异）。所有文本均验证UTF-8，不能默默替换非法字节。

## 小容器和大容器

设偏移宽度w：小容器w=2，大容器w=4。count和size各占w字节；size包含容器payload，**不包含父目录/根标签中的type**。对象的键目录项是w字节offset加2字节key_length；值目录项是1字节type加w字节offset或inline值。

literal、int16、uint16始终inline；int32、uint32只在大容器中inline。其余类型走offset。inline位置中的数字不是指针；例如true的01不能解释成跳到偏移1。

对象键按UTF-8字节长度、再按字节序排序；类型树使用有序Members，避免Go map重新排列存储顺序。键长度始终2字节，即使大对象也不扩成4字节。检查重复/乱序键和非法UTF-8，不静默覆盖前一个成员。

### 逐字节解码真实数组

json_lesson的id=23，record origin=1074，doc在页内1091、文件66627；payload基准为页内1092。字段总长31字节：

```text
02 | 04 00 | 1e 00
     count4   payload size30
05 01 00 | 07 10 00 | 09 14 00 | 0c 1c 00
int16=1    int32→16    int64→20    string→28
00 80 00 00 | 00 00 00 80 00 00 00 00 | 01 78
  32768             2147483648             "x"
```

1. payload头占4字节，4个值目录项占12字节，实际数据从payload偏移16开始。
2. 第一项直接inline整数1，不消耗后方payload。
3. 第二项到页内 `1092+16=1108` 取4字节，得到32768。
4. 第三项到payload偏移20取8字节，得到2147483648。
5. 最后一项到偏移28读取长度01、字符78，得到字符串x。

结果是 `[1,32768,2147483648,"x"]`。同一32768进入大数组时可inline，不能把“小数组必须外置int32”的规则套到所有容器。

## 返回模型及普通视图

用户已确认类型树方案：[json.go](../../json.go)定义JSONValue、JSONMember和JSONOpaque。主要字段为Kind、BinaryType、Value、Members、Elements、Opaque。

- SQL NULL：`Record.Values[i] == nil`。
- JSON null：`JSONValue{Kind:"null", BinaryType:4}`，不是nil。
- integer/unsigned/double：Value分别为int64/uint64/float64；string、boolean对应string/bool。
- object/array：有序Members/Elements；空容器仍由Kind明确标识。
- decimal/date/time/datetime/timestamp/opaque：保留Opaque.FieldType、独立Data字节和已知类型的Decoded值。

BinaryType保留整数宽度及小/大容器区别。“无损”指存储值和类型不丢失，不承诺重建用户插入前的空白、重复键、数字写法或原始JSON文本，也不提供物理文件重写器。

```go
value := record.Values[column].(innodb.JSONValue)
text, err := value.JSON() // json.RawMessage，普通JSON视图
```

默认 `json.Marshal(value)` 显示类型树；`value.JSON()`才输出普通JSON。普通视图中DECIMAL表现为精确数字、日期时间表现为字符串，这会丢掉类型区分，所以不能拿普通视图反推原树。接收JSON数字的程序也应使用UseNumber或其他精确数值方式。

## 接入和边界

`Column{Type:"JSON"}`不接受unsigned、长度、precision/scale等额外解码参数；类型和精度在字段内部。SDI DD type31、binary collation63及类型容量经检查后生成JSON schema。`variableValue`是页内和LOB拼接后的共同入口，不对二进制JSON整体做UTF-8检查。

`DecodeJSON`可单独解释完整字段字节；错误时返回零值，Read/ReadAuto错误时不返回部分行。单文档上限16MiB（含根标签）、节点总数100000、树深度100（根计1）；它们是本实现的保护边界，不是全部MySQL格式极限。

本阶段限定初始、无内部空洞的布局。容器数据按目录顺序连续验证，越界/重叠报ErrCorrupt，跳过空洞或多余容器尾部报ErrUnsupported。不实现JSON_SET等原地更新的空间复用，也不证明快照事务一致性。MySQL源码对某些宽松模式产生的零字节JSON有兼容回退；本实现明确拒绝空字段，不把它伪装成正常 `04 00`。
