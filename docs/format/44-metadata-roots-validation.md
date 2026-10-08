# 44. 用实际页面验证 SDI 索引入口

本章承接 [自动schema](43-sdi-schema.md)，学习根页的来源、不同ID的含义，以及自动读取应如何验收。实现、离线验证和真实建删索引验收均已完成，下面明确区分证据来源。

## 一个文件里有多种 ID

真实 `testdata/mysql8045/lesson_rows.ibd` 提供以下值：

| 对象/字段 | 值 | 用途 |
|---|---:|---|
| SDI Table记录键 | (1,424) | SDI树中的对象键 |
| SDI Tablespace记录键 | (2,45) | 另一个SDI对象键 |
| Table.se_private_id | 1102 | InnoDB表ID |
| Tablespace.se_private_data.id | 40 | 物理space ID |
| PRIMARY.se_private_data.id | 249 | 用户索引ID |
| PRIMARY.se_private_data.root | 4 | 用户索引根页号 |
| 页0 SDI根 | 3 | 元数据树根页号 |

这些值不能互换。SDI对象键中的45不等于物理space40，Table的1102不等于索引249；页3里的SDI记录也不是用户行。

索引的真实私有属性摘录：

```text
id=249;root=4;space_id=40;table_id=1102;trx_id=25424;
```

本地官方源码 `storage/innobase/include/dict0dd.h:247–265` 定义索引私有键；`sql/dd/types/index.h:58–74` 定义索引类型和算法；`sql/dd/types/index_element.h:51` 定义order=2为升序、3为降序。源码根与上一章相同。

root是页号；文件字节位置要乘当前16KiB页长。不能将某一个样本里root=4变成固定规则，也不能由索引创建序号推算页号。

## 从 SDI 到实际页头

以下字节直接提取自交付的lesson_rows，未通过合成样本替代。所有多字节整数均为大端。

| 页号 | 页内区间 | 文件绝对偏移 | 原始十六进制 | 含义 |
|---|---|---:|---|---|
| 0 | [54,58) | 54 | 00 00 40 21 | FSP flags=16417 |
| 0 | [10505,10513) | 10505 | 00 00 00 01 00 00 00 03 | SDI版本1、SDI根3 |
| 4 | [4,8) | 65540 | 00 00 00 04 | FIL页号4 |
| 4 | [8,16) | 65544 | ff ff ff ff ff ff ff ff | 根页左右兄弟均FIL_NULL |
| 4 | [24,26) | 65560 | 45 bf | FIL_PAGE_INDEX=17855 |
| 4 | [34,38) | 65570 | 00 00 00 28 | space ID=40 |
| 4 | [64,66) | 65600 | 00 00 | level=0，根本身是叶子 |
| 4 | [66,74) | 65602 | 00 00 00 00 00 00 00 f9 | index ID=249 |

例如索引ID字段绝对偏移是 `4×16384+66=65602`，读取8字节得到249。这里66是页内偏移，不是记录相对偏移。本章还没开始解析用户记录；记录解码继续使用前面章节的方法。

```text
SDI Tablespace.id=40 ─┬─ SDI所在表空间
                     ├─ Table索引space_id=40
                     └─ 根页FIL.space=40
SDI index.id=249 ──────── 根页PAGE_INDEX_ID=249
SDI index.root=4 ──────── 读取第4页 → FIL.page=4
SDI flags=16417 ──────── 页0 FSP.flags=16417
```

实现还检查索引table_id、Tablespace名称引用和文件ID一致，索引ID/根页不重复；实际读取页先通过CRC/LSN校验，再验证页类型、index ID、level和根兄弟指针。`RootVerified=true`表示该入口页经过这些核对，**不表示此索引全部记录已扫描**。

对于元数据可解释的普通二级B-tree索引，也可以报告根页和核对入口；存在二级索引时本阶段仍不给可读Schema。后续二级索引解码必须有单独实现与真实验收，不能把检查了页头等同于解析了二级记录。

## 已完成的离线证据

[metadata_test.go](../../metadata_test.go) 使用 [SDI manifest](../../testdata/sdi/manifest.json) 中全部158个原始资产：

- 自动schema与各资产既有手工schema逐字段一致，包括所有已经支持的类型、字典、主键不在首列的情况、外部值与多层树。
- `ReadAuto`和原`Read`完整结果一致；157个成功资产共27445行，另一个16MiB超限资产保持明确拒绝，无部分结果。
- 全项目原有测试继续与独立SQL预期对照；不是仅用两个相同算法互相证明。
- 自动CLI和显式CLI在158资产上逐字节对照JSON Lines，成功行27445，拒绝1；大整数、负零和二进制Base64没有经过浮点中转。

错误测试覆盖关键字段缺失、版本、引擎、INSTANT属性、生成表达式、未知类型/排序规则、根越界、index ID错配、列下标越界、降序键、物理列顺序、私有键转义/重复/溢出和字典Base64/UTF-8/序号。公开ReadAuto入口还通过完整SDI树测试不支持版本及CRC损坏的无部分返回契约。

另有两种明确标记为**合成**的证据：

1. 把真实用户根页副本追加到页7，修改副本FIL页号/CRC及解析后的SDI root，再使用生成schema读出原有行，证明实现不固定根4。
2. 添加一个带新index ID的根页副本及二级索引元数据，验证报告两个入口、拒绝生成可读schema。其页体不是真正的二级索引记录，不计作真实二级读取证据。

本轮test/race/vet通过，核心包语句覆盖率93.1%；FuzzMetadata在10秒预算完成11674次执行，无失败输入。以上为首轮离线验证记录；本轮已补齐真实生命周期测试，并纳入默认离线回归。

## 使用与复跑

```sh
# 自动读取，不再要求单独的schema.json
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/auto testdata/mysql8045/lesson_rows.ibd

# 只查看报告；有Issues时会看到Schema=null
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/auto -metadata testdata/mysql8045/lesson_rows.ibd

GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzMetadata$' -fuzztime=10s -parallel=2
```

输出仍为 `[-3,42,"InnoDB"]` 等按主键排列的JSON行。`.ibd.gz`由调用方解压成临时文件后读取，gzip不是InnoDB页格式。报告中的原DD字段与Schema中的解码参数含义不同，特别是CharLength和Unsigned，参见上一章。

## 已完成的真实索引生命周期验收

首次连接被审批检查点兼容性问题阻塞；2026-09-15用户要求重试后，提权连接成功，确认8.0.45/16KiB/crc32。新库为 `innodb_reader_metadata_2b94e27b44eb`，未重启实例、未改全局配置或既有用户表。五个快照已保存到 [testdata/metadata](../../testdata/metadata/manifest.json)，官方工具版本、SHA256和CLI退出结果见 [verification.json](../../testdata/metadata/verification.json)。

已执行 [生成脚本](../../scripts/generate_metadata_fixtures.py)：新建随机独立库、插入四行，依次采集baseline、added、dropped、recreated、final五个快照。每个快照在**同一持久连接**执行FOR EXPORT、查询SQL预期/字典、复制文件、UNLOCK；随后对副本运行官方ibd2sdi与严格crc32检查。脚本不删除原有库，不修改全局配置，新建库保留供检查；新增/删除的索引只属于本次新表。

重新采集时，在已设置 `MYSQL_PWD` 的环境运行（密码不写入命令文档或资产）：

```sh
python3 scripts/generate_metadata_fixtures.py \
  --mysql /Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql \
  --socket /Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock \
  --ibd2sdi /Users/kimihiro/workspace/software/mysql-8.0.45/bin/ibd2sdi \
  --innochecksum /Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum \
  --out /tmp/innodb-metadata-lifecycle

INNODB_METADATA_FIXTURES=/tmp/innodb-metadata-lifecycle \
  GOCACHE=/tmp/innodb-go-build-cache go test -run '^TestMetadataLifecycle$' -v
```

输出目录必须不存在。脚本记录版本、页长、checksum配置、会话sql_mode、SQL、SHOW CREATE TABLE、INNODB_INDEXES查询结果、原始SDI、SHA256和读取期望。未完成采集的manifest保持incomplete，不得当成功资产。

`TestMetadataLifecycle` 默认读取交付的testdata/metadata，普通go test即执行，不再Skip；环境变量用于验收新采集目录。它逐项核对SQL索引身份、独立DDL列定义及官方SDI对象，在无二级索引三个快照比较四行SQL预期，有二级索引的两个快照检查明确拒绝。五个文件的innochecksum严格crc32检查、10个SDI对象及CLI输出对照全部通过。

### 真实页号复用与索引身份

下表为本轮实际SQL字典与页面核对结果，所有快照space ID=194、table ID=1256；主键始终index ID=403、root=4。

| 快照 | 操作后的二级索引 | 二级index ID | 二级root | ReadAuto |
|---|---|---:|---:|---|
| baseline | 无 | — | — | 四行与SQL一致 |
| added | idx_score | 404 | 5 | 明确拒绝，报告两个根 |
| dropped | 无 | — | — | 四行与SQL一致 |
| recreated | idx_score | 405 | 5 | 明确拒绝，报告两个根 |
| final | 无 | — | — | 四行与SQL一致 |

这里实际发生的是**同一页号被新索引复用**，不是主键根搬移。added第5页的 `[66,74)` 为 `00 00 00 00 00 00 01 94`（404）；recreated同一位置为 `00 00 00 00 00 00 01 95`（405）。两个字段的文件绝对偏移均为 `5×16384+66=81986`，都是8字节大端。`[34,38)` 为 `00 00 00 c2`，即space194。

更容易误判的是dropped/final：第5页仍有页类型字节 `45 bf`，但index ID字段已为八个零，SDI中也已无idx_score。这是本次快照观察值，不能推广为所有版本/时机的释放页清理规则。它说明只扫描页类型或缓存旧root=5，不能可靠还原当前索引目录。实现从最新SDI枚举索引，并核对实际index ID；不会把遗留页头误当现存索引。

baseline文件114688字节，之后四个文件131072字节。删除索引并没有令这些快照文件缩回初始长度；文件大小也不能作为当前索引数量的判据。主键根搬移仍由前文合成测试单独验证，不把此次真实复用结果说成真实搬根。

本轮新增5资产，其中3个可读快照共12行、2个二级索引拒绝快照；累计163资产，按快照计160个成功资产27457行，另有1个超限拒绝与2个二级索引拒绝。第二十阶段验收已完成，下一候选阶段是用户二进制JSON值解析，本轮未启动。
