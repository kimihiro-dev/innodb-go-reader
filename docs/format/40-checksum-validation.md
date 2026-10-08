# 40 页校验验证：真实资产、损坏副本与结构测试

本章承接 [校验公式](39-page-checksum.md)，说明为什么校验测试与记录结构测试必须分开，以及如何独立复现本阶段结果。

## 两组测试各自证明什么

[checksum_test.go](../../checksum_test.go) 的校验测试不修改 CRC 来掩盖损坏：

- 校验原始页常量，并以不用生产查表逻辑的逐位 CRC32C 计算作为独立预期。
- 修改头部、页体、页类型、校验字段和尾部 LSN，要求 ErrCorrupt 与页号/文件偏移。
- 即使重算 CRC，头部 LSN 与尾部低32位不一致仍须失败。
- 对 FSP、根页、非根叶子、LOB_FIRST、LOB_DATA、LOB_INDEX 分别修改正文，验证它们确实进入检查路径。
- 引用全零页失败；未访问的 SDI 损坏不影响当前树读取，明确“访问路径校验”的范围。
- [26,38) 的变更本身不改变 CRC；修改 space ID 仍由结构归属检查拒绝。

原有 record/tree/LOB 损坏测试检查的是更深一层的结构。加入 CRC 后，如果仍直接翻转记录字节，它们会全部提前停在 CRC 层，不能再证明循环链、错误长度、历史标志等检查有效。因此这些测试先修改内存副本，再调用测试专用 `resealTestPages` 更新 CRC 与尾部 LSN。

```text
校验测试：原始页 → 修改字节 → 不重算 CRC → 验证校验失败
结构测试：原始页 → 修改结构 → 重算 CRC → 验证更深层结构拒绝
真实回归：原始页 → 不改任何字节 → SQL 对照全部行
```

resealTestPages 只存在于测试中，不是库的修复 API，不写回交付文件。更新已有测试是为了保留原验证意图，不是通过重新计算校验和证明损坏数据合法。FuzzChecksum 覆盖原始校验入口，FuzzRead 与原有类型 fuzz 重封装后继续触达记录解析。

## 官方工具独立验证

本阶段复用全部 158 个已有资产，未新增数据库或修改原始文件。所有文件解压到临时目录后，执行 MySQL 8.0.45 官方 `innochecksum --strict-check=crc32`，全部通过。其中 157 组是成功行读取资产，另一组仍是超过 16 MiB 的值拒绝样本；校验正确不代表值容量属于支持范围。

另对 lesson_rows 临时副本做四类单字节修改：头部 CRC、正文、尾部 CRC、尾部 LSN，官方工具均拒绝。使用官方工具在另两份临时副本中转换为 none 和 innodb 算法，并用各自算法验证成功；当前 Go CLI 则按严格 CRC32C 契约拒绝：

| 临时副本 | 页 0 头部 | 页 0 尾 CRC | 当前计算 CRC32C | Go 结果 |
|---|---|---|---|---|
| 原 crc32 | 418a386e | 418a386e | 418a386e | 成功 |
| 官方转换 none | deadbeef | deadbeef | 418a386e | 明确拒绝 |
| 官方转换 innodb | 12037ffb | ba57b8de | 418a386e | 明确拒绝 |

这里只报告临时验证结果，不把这些值作为判断所有旧算法文件的规则。

## 离线复现

```sh
GOCACHE=/tmp/innodb-go-build-cache go test ./...
GOCACHE=/tmp/innodb-go-build-cache go test -race -cover ./...
GOCACHE=/tmp/innodb-go-build-cache go vet ./...
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzChecksum$' -fuzztime=10s -parallel=2
GOCACHE=/tmp/innodb-go-build-cache go test -run '^$' -fuzz '^FuzzRead$' -fuzztime=10s -parallel=2

/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum \
  --strict-check=crc32 testdata/mysql8045/lesson_rows.ibd
GOCACHE=/tmp/innodb-go-build-cache go run ./examples/read \
  testdata/mysql8045/lesson_rows.ibd testdata/mysql8045/lesson_rows.json
```

可用以下代码只在临时目录复现损坏，不改原文件：

```python
from pathlib import Path
import subprocess
import tempfile

source = Path('testdata/mysql8045/lesson_rows.ibd').read_bytes()
with tempfile.TemporaryDirectory(prefix='innodb-crc-') as directory:
    damaged = bytearray(source)
    damaged[4 * 16384 + 176] ^= 1
    target = Path(directory) / 'damaged.ibd'
    target.write_bytes(damaged)
    result = subprocess.run([
        '/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum',
        '--strict-check=crc32', str(target)
    ])
    assert result.returncode != 0
```

## 结果（2026-09-15）

全部 157 组成功夹具、27445 行及原有拒绝样本回归通过；158 文件官方严格校验通过。race/cover/vet 通过，核心覆盖率 98.0%。10 秒 FuzzChecksum 完成 602586 次执行，重封装校验和后的 FuzzRead 完成 459349 次执行，均无失败。

本阶段无 MySQL 服务操作，不要求实例在线。未修改测试数据库、实例配置或夹具字节。校验范围限实际访问的当前格式页面；自动元数据提取仍待第十九阶段。
