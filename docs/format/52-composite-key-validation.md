# 52：元组比较、MIN_REC 与复合树验收

复合键正确解码后，仍需保证每一行落在正确的子树范围中。只比较第一个成员会丢掉大部分约束；将多个整数压成一个浮点数或相加也不能保留索引顺序。

## 1. 按第一个不同成员决定顺序

对同一 schema 的键逐位置比较：

```text
(0,4,4) < (0,4,5)      第三个成员决定
(0,4,100) < (0,5,0)    第二个成员决定
(-1,max_uint64) < (0,0) 第一个成员已经决定，后续不再比较
```

每个位置有自己的整数类型和 signed/unsigned 属性。第二十四阶段使用 `integerOrder` 映射为无溢出的 uint64 排序值；第二十五阶段扩展为原始编码元组（见[第54章](54-key-order-validation.md)），仍逐项比较、不跨位置混用类型。旧数值比较仅保留为测试编码辅助函数。

相同有符号位置的最小值到最大值、有无符号混合的不同位置、uint64 最大值都不经过 float64。十进制字符串拼接也不可替代数值比较，例如字符序中的“10”和“2”顺序不同。

所有成员都相同才是重复主键。页内用户记录和跨页记录都要求完整元组严格递增；非叶子有限导航记录同样检查顺序。

## 2. 有限导航键限定半开区间

某条导航记录的完整元组为 L，下一条为 H，则子树的有效键范围为 `[L,H)`；末条继承父级上界。内部任务保存可空 low/high 元组：nil 表示无界，不用某个实际整数冒充负无穷或正无穷。

```text
父页导航键 L                 下一导航键 H
       ↓                           ↓
       子树中的每个有限键必须满足 L ≤ key < H
```

树遍历会对实际读取到的每个有限键检查范围。不能仅在读取父页时检查 L<H：父页顺序正确，子页也仍可能挂到了错误位置。

真实 `composite_tree` 的一段根页导航：

```text
MIN_REC → child 5
(0,4,4) → child 13
(0,9,7) → child 9
```

因此 child13 子树要求 `[(0,4,4),(0,9,7))`。共享第一个成员0时，第二、第三成员仍参与上下界；页号13到9的下降不表示键序下降。物理页分配顺序不能代替索引链顺序。

## 3. MIN_REC 的存储键不是有限下界

这是本轮最直观的真实证据：`composite_tree` 根页4首条导航在 origin=126，原始字节：

```text
00 | 10 00 11 00 90 | 80 00 80 00 80 00 00 02 | 00 00 00 05
NULL  MIN_REC记录头      存储键(0,0,2)              child5
```

但是 child5 实际第一条用户键是 **(0,0,0)**，位于页内 origin=8805；下一条 **(0,0,1)** 的 origin=128。记录链的键序既不要求 origin 递增，也不要求首条叶子键等于 MIN_REC 中存储的值。

如果把 `(0,0,2)` 当作有限下界，将错误拒绝前两行。MIN_REC 表示左侧无穷边界或继承父任务的下界，其存储字节仍公开保留供分析，但不作为有限 lower bound。当前规则只允许每层最左页的首条非叶子记录带该标志，具体检查沿用既有树契约。

## 4. 真实三层、16成员共享前缀

[composite_deep](../../testdata/composite/composite_deep.ibd.gz) 使用16个 BIGINT 主键成员，符号属性交替，前15个成员都为零，第16个为0..2999；普通 VARCHAR 载荷为1200字节。共有3000行、278个聚簇页，根页4的 Level=2，即根/中间/叶子三层。

每个完整键宽为 `16×8=128`，非叶子数据部分为132字节。根页第二条导航位于 origin=264，End=396；其前15成员为零，末成员=632，指向页38。该成员和子页号的真实字节：

```text
页内384..391：00 00 00 00 00 00 02 78   第16成员 UNSIGNED 632
页内392..395：00 00 00 26               child38
```

根页首条 MIN_REC 指向页37；第三条键末成员=1919，指向页39。遍历能够正确恢复所有前15成员相同的行，说明第16成员参与了叶子、非叶子和跨层范围检查；仅依赖第一个整数不能通过本样本。

叶子第一行位于页5 origin=128，键 `[128,256)`，事务ID `[256,262)`，回滚指针 `[262,269)`，普通文本 `[269,1469)`。记录前的 `b0 84` 逆向解码为1200字节长度；不能把128字节复合键当成变长字段。

## 5. 验收矩阵与证据

本轮新库 `innodb_reader_composite_25c6d3d89012`，MySQL 8.0.45、16KiB、DYNAMIC、crc32。每表保持同连接导出锁期间查询 SQL 和复制文件；原实例继续运行，未改既有表或全局配置。

| 快照 | 行数 | 目的 |
|---|---:|---|
| composite_lesson | 8 | 主键与声明顺序不同、共享前缀、SMALLINT/MEDIUMINT/uint64端点 |
| composite_empty | 0 | 空复合主键表与自动 schema |
| composite_types | 3 | 十个成员包含五种整数宽度、有无符号；主键顺序反转 |
| composite_lob | 3 | 三列混合整数键、NULL/空及60000字节页外文本 |
| composite_tree | 600 | 随机插入、三个成员参与排序、13页两层树 |
| composite_deep | 3000 | 16列键、前15成员共享、278页三层树 |

共6组快照3614行，一处真实页外字段。SQL 预期按全部主键 `ORDER BY`，返回列按原声明序；测试用精确 JSON 数值比较，保留 uint64 最大值。页外样本引用从叶子 `origin+13+13` 开始，第一段13是复合键宽，第二段13才是系统字段宽。

[composite_test.go](../../composite_test.go) 验证：

- 自动/手工 schema 和完整结果相等，原始资产 SHA256。
- 每行完整 SQL 对照、物理键顺序、事务字段位置、非叶子成员类型及子页偏移。
- 每条导航截断一字节必须拒绝；重复/缺失/可空/非整数/超过16成员的 schema 拒绝，单列新旧写法结果相同。
- 复制完整键制造重复；只修改第三成员制造倒序；修改有限导航键最后成员，或修改叶子键，使其越过父区间。范围测试明确要求触发 `outside parent range`，防止其他校验意外掩盖漏检。
- SDI 中 DESC、前缀长度、重复键列和系统字段顺序错误拒绝。

结构损坏只修改内存副本并重算 CRC，再经 Read 和 ReadAuto 验证错误且无部分返回。既有201资产回归覆盖单列兼容；真实多层证据与合成损坏测试分别记录，不把篡改文件冒称为数据库导出。

## 6. 复现与交付边界

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzCompositeOrder$' -fuzztime=10s
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzCompositeRead$' -fuzztime=10s

gzip -dc testdata/composite/composite_lesson.ibd.gz > /tmp/composite_lesson.ibd
go run ./examples/auto /tmp/composite_lesson.ibd
```

六文件通过官方严格 crc32 校验，12个 SDI 对象与 ibd2sdi/项目示例一致，3614行命令行输出与 SQL 一致，见 [verification.json](../../testdata/composite/verification.json)。官方输出随夹具保存并加入默认离线回归。生成器 [generate_composite_fixtures.py](../../scripts/generate_composite_fixtures.py) 保存真实 SQL、DDL、schema、预期及索引身份，密码只从 MYSQL_PWD 环境变量读取。

阶段24支持1..16个升序、非NULL、完整整数列；没有实现字符排序规则、DESC、前缀键、二级索引、无显式主键、事务可见性或更新历史。读取仍限定此前支持的稳定快照及页面/LOB格式。下一阶段的字符串及其他聚簇键需要单独建立比较契约。

本轮完整 race 回归与 vet 通过，核心覆盖率93.6%；10秒预算FuzzCompositeOrder完成2580325次、FuzzCompositeRead完成537次，无失败输入。累计207组资产，204组成功资产33399行及3组既有拒绝资产。
