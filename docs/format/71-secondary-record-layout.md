# 71 二级索引记录、前缀与聚簇定位字段

本章目标：辨认普通二级叶子和非叶子记录，解释为什么二级记录不能作为完整表行返回，并从真实字节找出前缀和聚簇定位信息。前置为[聚簇索引身份](55-clustered-identity.md)、[多类型键](53-typed-key-layout.md)、[查询导航](69-key-query-navigation.md)。版本、页大小和稳定快照边界沿用此前章节。

## 一、叶子保存索引字段，不保存完整表行

普通聚簇叶子包含完整物化行，带DB_TRX_ID和DB_ROLL_PTR；普通二级叶子只包含二级字段与定位聚簇记录需要的字段，没有这两个逐记录事务字段。本阶段返回独立SecondaryRecord，Values按物理Fields排列，不为未存储表列补nil。

```text
二级叶子（从左到右为地址增加，示意）
[逆序变长长度][NULL位图][5字节记录头] | 字段0 | 字段1 | ... |
                                      ↑ origin

二级非叶子
[逆序变长长度][NULL位图][5字节记录头] | 完整物理键 | 4字节子页号 |

完整物理键 = 二级声明字段 + 尚未完整包含的聚簇定位字段
```

这是物理布局说明，不包含LOB引用、事务系统列或完整行补全。此前的CRC、页身份、堆、活动链/free链、目录owner与garbage核算继续使用。二级叶子可以比聚簇导航记录更短，因此页头粗略记录数检查允许6字节下限；真正字段边界仍逐记录核验。

| 字段 | 相对origin位置/长度 | 含义 |
|---|---|---|
| 记录头 | −5..−1，5字节 | owner/info、heap/status、相对next偏移 |
| NULL位图 | 记录头前，ceil(可空物理字段数/8) | 按物理字段序分配bit，NULL不消费载荷/长度项 |
| 变长长度 | 位图前，逆序存储 | 非NULL变长字段的一/两字节长度 |
| 叶子载荷 | origin起，各字段依次排列 | 原类型编码，NULL与空字节必须区分 |
| 非叶子child | 完整键之后，4字节大端 | 子页号，不能为0 |

NULL在ASC中排在非NULL之前，DESC反转。比较器先处理NULL，再比较对应类型原始键字节和PAD SPACE，最后应用成员方向。完整物理键严格递增；唯一二级索引允许用户键含NULL时出现多条记录，不能用SQL唯一成员数截断物理排序键。

## 二、聚簇后缀的去重规则

本地MySQL8.0.45源码的 `storage/innobase/dict/dict0dict.cc:3206–3267` 构建内部二级索引：先复制用户字段，只把完整字段记为“已有”，再追加缺失的聚簇键成员。隐藏聚簇索引追加六字节ROW_ID；用户聚簇键按原成员顺序和方向追加。已经完整存在的成员复用其二级字段位置，方向保持二级声明，不重复存储。

重要差异：SDI的构造规则不同。`storage/innobase/handler/ha_innodb.cc:14949–14978` 按列身份去重，所以同一列已有前缀时，SDI可能不再列出引擎实际追加的完整副本。不能直接把IndexMetadata.Elements当完整物理字段数组。

真实 `overlap` 表：

```sql
PRIMARY KEY(code DESC,id),
KEY prefix_idx(code(2)),
KEY overlap_idx(id DESC,code)
```

| 索引 | SDI元素 | 实际物理Fields | ClusteredFields |
|---|---|---|---|
| prefix_idx | code前缀、隐藏id | code前缀ASC、完整code DESC、id ASC | `[1,2]` |
| overlap_idx | id DESC、完整code ASC | id DESC、code ASC | `[1,0]` |

因此完整聚簇定位键与二级排序顺序是两个概念。ClusteredFields引用实际Fields，不依赖名字去重或数组末尾一定连续。PrefixBytes描述声明上限，不说明每条原值一定被截断；短原值也仍属于前缀字段，不能据此将它当覆盖的完整列。

## 三、真实字节：NULL短记录

`testdata/secondary/tiny.ibd.gz` 的n_idx根页5也是叶页。首条记录对应n=NULL、id=−90。其Start=120、origin=126、End=127，文件载荷绝对偏移为 `5×16384+126=82046`。

```text
页内120       121..125       126
01            00 00 10 00 17 26
NULL位图      五字节头       有符号TINYINT id
```

`01`说明可空n为NULL，所以载荷直接从id开始。`26 XOR 80 = A6`，按有符号八位解释为−90。整条记录只有7字节。`00 10`中的heap=2、status=0；next相对偏移0x17=23，指向页内149。这里不存在13字节事务字段；若按聚簇行布局读取会越过记录边界。

## 四、真实字节：同列前缀和完整副本

`overlap.ibd.gz` 的prefix_idx首条记录位于页5：Start=2500、origin=2507、End=2520，文件载荷绝对偏移84427。code值为 `ab00119`，id=119。

```text
页内2500..2506：07 02 | 00 03 c8 ff ec
                    长度数组 | 记录头
页内2507..2519：61 62 | 61 62 30 30 31 31 39 | 80 00 00 77
                 ab  |       ab00119        |     119
```

长度数组在记录头之前逆序存储：从头前向后读到02，再读07，分别对应物理字段0和1。此表两个键字段非NULL，没有NULL位图。载荷中code出现两次，第一次只有两个字符，第二次是完整定位字段。`ff ec`作为相对next偏移按16KiB页内回绕解释；记录的逻辑顺序与物理地址增长方向不必相同。

完整code字段继承聚簇DESC，使相同`ab`前缀的记录先输出较大的code；并不是把code显示字符串倒序。成员原编码与方向仍分别保存和比较。

## 五、真实字节：128字节前缀的两字节长度

`wide_prefix`声明VARCHAR(400) latin1_bin，但索引只存前200字节。id=2的原值恰好128字节，位于页5、origin=280，文件载荷绝对偏移82200。

```text
页内272..279：80 80 | 00 | 00 00 20 00 8c
              长度  位图   五字节头
页内280起：78 78 ...（125个x）... 30 30 32 | 80 00 00 02
```

从位图前向低地址读到高位长度字节0x80，再读0x80，实际长度为128。是否采用两字节格式取决于原列最大容量400是否大于255，而非前缀容量200。`storage/innobase/rem/rec.cc:114–143` 的DATA_BIG_COL逻辑直接使用原列信息。若拿前缀容量判断，会把0x80误作单字节长度，并把另一字节算进记录外碎片或位图，造成后续字段/顺序校验错误。

实现先按原列容量读取长度，再核验不超过前缀声明上限。COMPACT的VARBINARY(300)前160字节样本也走同样分支。字符前缀还要检查字符编码及边界：本阶段PrefixBytes以最大编码字节数表示，字符前缀长度为它除以charset最大字节宽度。

真实deep.b_idx根页5为level2，首个MIN_REC导航项origin=130、End=1048，头为 `10 00 11 24 58`。child位于页内1044..1047（文件绝对偏移82964），字节 `00 00 00 55` 表示子页85。载荷包含全部二级字段及聚簇后缀后才出现child，不能按用户字段数提前读取。

## 六、公开模型与代码入口

- SecondarySchema：Name、Unique、RowFormat及索引/空间/根身份；Fields是实际物理序，UserFields是用户声明成员数，ClusteredFields是聚簇键顺序到Fields的映射。
- SecondaryField：Column是当前物化表列下标，ROW_ID用−1；Definition保留原列类型和本索引方向，PrefixBytes=0表示完整字段，RowID标明隐藏身份。
- SecondaryRecord：Values、FieldBytes、ClusteredKey/RowID、DeleteMarked、Raw及PageNumber/Start/Offset/End/Header/HeapNumber/NextOffset。FieldBytes保留完整物理字符字节；Values中CHAR按原约定去掉尾空格。
- SecondaryNode：独立导航项，含相同键载荷来源、ChildPage和Minimum。最左MIN_REC继承父下界，不把保存的显示值当有限边界。

`secondary_schema.go:InspectSecondary` 从已核验SDI选择普通BTREE二级索引并重建字段列表。`secondary.go:decodeSecondary` 解释无事务字段记录，`compareSecondary`处理NULL/方向，`walkSecondary`检查整树。页结构仍由 `record.go:decodePageEntries` 与 `page.go` 共用，避免另一套目录/堆检查器。

本阶段只解析受支持普通BTREE，字符排序仍限已验证的四个_bin。前缀限CHAR/VARCHAR/BINARY/VARBINARY；TEXT/BLOB前缀、索引VIRTUAL、函数/多值/空间/全文索引尚未支持。独立二级模型不恢复MVCC可见性，也不进行回表；第35阶段再实现检索与完整行取值。
