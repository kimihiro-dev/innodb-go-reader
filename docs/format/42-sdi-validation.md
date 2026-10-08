# 42. SDI：页外压缩载荷与官方验证

本章承接 [SDI 记录](41-sdi-records.md)，从大对象引用一直追踪到 JSON，并区分真实存储证据、合成结构测试和当前支持范围。

## 专用 SDI_BLOB 链

SDI JSON 压缩后仍可能过大，需要离开 SDI 叶子页。当前非压缩表空间使用类型18的 SDI_BLOB 链，而不是用户长字段的 LOB_FIRST/LOB_INDEX。这里“非压缩表空间”不等于“JSON 未压缩”：页本身未压缩，页体携带的仍是 zlib 流的一部分。

本地官方源码依据：`utilities/ibd2sdi.cc:1561–1625` 读取 SDI_BLOB 链，`1835–1960` 解释记录引用与解压；`storage/innobase/include/fil0fil.h:1265` 定义类型18。源码根见上一章。

```text
SDI叶子：长度 c014 → 本地20字节引用
                         ↓ first page
SDI_BLOB页：part_len(4) → next_page(4) → zlib片段
                         ↓
                    下一SDI_BLOB页 … FIL_NULL
                         ↓
         完整压缩载荷 → zlib校验/解压 → 原始JSON对象
```

每个页的 part_len 在 `[38,42)`，next_page 在 `[42,46)`，数据从46开始，最多 `16384−8−46=16330` 字节；末尾8字节仍是 FIL trailer。所有页都经过当前读取路径的 CRC 和 LSN 校验。

## 真实大 ENUM 元数据

[enum_65535](../../testdata/enums/enum_65535.json) 含65535个枚举标签。SDI 记录 `(1,536)` 在页3，Start=441、origin=448、End=501：

```text
14 c0 | 00 00 18 fe bf
external20   记录头
```

原始长度为2800140字节，压缩长度469961字节。引用位于页内 `[481,501)`：

```text
00 00 00 97 | 00 00 00 05 | 00 00 00 26 | 00 00 00 00 00 07 2b c9
 space 151     first page 5    offset 38        页外长度469961
```

这里引用的 offset=38 是 BLOB 页头位置，不能把它误解为用户新式 LOB 引用中的版本号1。当前真实样本没有本地前缀，记录只保留20字节引用。

页5的 `[38,46)` 为 `00 00 3f ca 00 00 00 06`：本页片段16330字节，下一页6。第一段载荷的文件偏移为 `5×16384+46=81966`。页5..32各16330字节，页33为12721字节，总和：

```text
28×16330 + 12721 = 469961
```

先按链顺序拼接这469961字节，再解压得到2800140字节JSON。每页不是独立zlib流，不能逐页解压。29个块的 PageNumber、Offset、Length 保存在 External.Chunks 中，它们对应压缩字节，不是JSON中的偏移。

读取器检查空间归属、引用偏移、页号边界、页类型、片段长度、链循环、提前终止和多余后继。长度和引用高标志不符合当前契约时拒绝，不允许依赖无符号溢出绕过长度限制。源码中涉及768字节本地前缀的分支也已实现并用合成输入测试；当前158份真实资产只观察到纯20字节SDI引用，不能宣称已取得真实前缀样本。

## 解压后的完整性

zlib使用标准库compress/zlib，长度受限读取至声明原始长度加一字节，用于识别超出声明长度的输出。必须同时满足：

- 解压与流校验成功，输出长度精确相等。
- 压缩输入被完整消费，没有尾随垃圾或第二个拼接流。
- 输出是有效UTF-8、合法JSON，且顶层为对象。

RawMessage保留原始空白与数字文本；示例程序以JSON Lines重新序列化外层包装时可能调整空白，但对象语义不变。json.Valid不能代替MySQL二进制JSON解码，这里得到的是SDI中的文本JSON，与计划中的用户JSON列解析不同。

## 树结构与测试证据

复用现有页目录、系统记录、活动/free链、堆号及占用范围验证，SDI回调只解释固定字段和联合键。遍历还验证父子层级、键范围、同层双向链、重复页及跨页键顺序；不会只读取最左叶子后忽略父节点。

真实316个对象来自158份文件，每份有两个对象；其中315个载荷页内、1个跨29页。现有样本的SDI根均为单叶子，多层导航另用**合成**测试：复制真实记录体，构造一个非叶根和两个叶子，再搬移根到页9。该测试验证导航算法，不计入真实MySQL夹具数量。

[sdi_test.go](../../sdi_test.go) 同时覆盖：入口缺失/版本/越界、删除标志、长度上限、链循环、错误页类型/space、zlib截断/尾随数据、非法UTF-8/JSON、无部分结果；空根、单字节长度、768字节前缀和uint64键边界用明确合成输入验证。结构变异先重封装CRC以触达深层解析，另有未重封装输入验证SDI页确实受CRC保护。

## 官方预期与复现

[SDI manifest](../../testdata/sdi/manifest.json) 记录源资产路径、解压后SHA256、对象数与官方工具版本。158个expected.json.gz来自同版本ibd2sdi的实际输出，压缩保存约1.2 MiB，不复制原始.ibd。测试用UseNumber逐对象语义对照；全部316个对象和CLI输出均通过。

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzSDI$' -fuzztime=10s -parallel=2

GOCACHE=/tmp/innodb-go-build-cache go run ./examples/sdi testdata/mysql8045/lesson_rows.ibd
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/ibd2sdi testdata/mysql8045/lesson_rows.ibd
```

示例每行输出 `{type,id,object}`；官方输出外层是包含首项`"ibd2sdi"`的数组，比较时去掉该标记。需要物理来源时使用ReadSDI返回结构。

重新提取预期时选择不存在的目录：

```sh
python3 scripts/extract_sdi_fixtures.py \
  --ibd2sdi /Users/kimihiro/workspace/software/mysql-8.0.45/bin/ibd2sdi \
  --out /tmp/new-sdi-expectations
```

该脚本只使用已有文件和临时解压副本，不连接数据库、不需要密码。现有原始资产保持不变。

## 本阶段结果（2026-09-15）

158文件、316对象官方对照及CLI对照通过；用户行27445行和原拒绝契约回归通过。race/vet通过，核心覆盖率97.2%；10秒预算FuzzSDI完成258953次执行，无失败。手册同时记录真实单叶/页外数据与合成多层导航，不混淆验证来源。

本阶段没有启动或修改MySQL实例；只读取用户提供的官方源码。ReadSDI未生成用户schema，也不执行事务历史、压缩表空间、SDI_ZBLOB或删除记录恢复；这些边界仍明确拒绝。
