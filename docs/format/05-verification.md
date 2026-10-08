# 05 验证与复现

学习目标：独立运行解析、理解验证证据，并重新生成自己的样本。前置知识：前四章；运行示例只需安装 Go。

## 1. 不启动 MySQL 也能读取

在项目根目录，使用 Go 1.25 或更高版本执行：

```sh
go test ./...
go run ./examples/read testdata/mysql8045/lesson_rows.ibd testdata/mysql8045/lesson_rows.json
```

实际输出：

```json
[-3,42,"InnoDB"]
[2,null,null]
[7,0,""]
[12,-5,"你好"]
```

输入 JSON 是明确的外部 schema，输出数组顺序与其 `columns` 顺序相同。Go 解析路径仅打开两个本地文件，不调用 mysql 客户端，也不读取 `.expected.json`。

若本机环境限制默认构建缓存目录，可设置 `GOCACHE=/tmp/innodb-go-build-cache` 后运行同样命令；这不改变文件解析行为。

## 2. 在 Go 中使用

可执行完整示例见 `examples/read/main.go`。核心流程为：

```go
// schema 已从可信 JSON 解码，file 已通过 os.Open 打开。
info, err := file.Stat()
if err != nil {
    return err
}
result, err := innodb.Read(file, info.Size(), schema)
if err != nil {
    return err
}
for _, record := range result.Records {
    fmt.Println(record.Offset, record.Values)
}
```

`Read` 接收 `io.ReaderAt`，因此测试可以传 `bytes.Reader`，正常使用可以传 `*os.File`。文件生命周期由调用方负责。单叶子页的记录结果直接返回列表，不需要在这个规模引入异步流或缓存。

`record.Values` 的具体类型为 `int32`、`string`、`nil`。`Record` 另外公开 Start、Offset、End、Header、HeapNumber、NextOffset、Transaction、RollPointer。`Result.Page` 公开本次叶子页的身份和基本结构字段。

错误分类：使用 `errors.Is(err, innodb.ErrUnsupported)` 判断已知不支持的 schema/布局，使用 `ErrCorrupt` 判断结构不一致；底层 I/O 错误用 `%w` 保留原因。错误时不返回部分结果。

## 3. 独立 SQL 基准如何避免自证

`TestMySQLFixtures` 从 manifest 读取案例，先验证 `.ibd` SHA256，然后解析物理记录，并与生成阶段由 MySQL 输出的 `*.expected.json` 比较所有行、所有列。

预期值不由 Go 解码器生成。两条路径独立：

```text
MySQL SQL 执行 → JSON_ARRAY 按 id 排序 → expected.json
同一导出锁期间 .ibd 副本 → Go 字节解析 → 实际结果
                                           ↓
                                       逐行逐列比较
```

此外，对事务 ID 与 roll pointer 进行原始字节保留检查。`TestRecordLocations` 固定验证贯穿案例的逻辑链，帮助手册与交付样本保持一致；重新生成样本后需重新核对这一测试的具体偏移，不能盲目假定永远相同。

| 真实案例 | 行数 | 验证重点 |
|---|---:|---|
| lesson_rows | 4 | 乱序插入、负数、NULL、空字符串、中文 |
| empty_rows | 0 | 系统记录直接相连，不返回虚构用户行 |
| single_row | 1 | 最小非空表与 emoji |
| boundary_rows | 6 | INT 极值、32 个 emoji、NULL 组合 |
| null_bitmap_rows | 2 | 10 个可空列、两字节位图、主键最后声明 |
| directory_rows | 40 | 同一页 9 个目录槽、owner 计数 |
| variable_rows | 4 | 多 VARCHAR、反向长度区、252 字节的一字节长度 |

## 4. 损坏与不支持输入的验证

测试在内存中修改夹具副本，不改交付 `.ibd`：

- 截断、非页对齐文件、越界根页、短读。
- space/index/page ID 不一致，错误页类型。
- 非叶子入口、兄弟页、REDUNDANT、压缩/加密/共享或其他页大小标志。
- heap/目录超界、系统记录错误、记录链越界/循环/提前结束。
- 重复 heap number、记录区重叠、目录 owner 错误、主键乱序。
- 删除/INSTANT/行版本标志、非普通记录状态。
- 超出 schema 的字符串长度、非法 UTF-8。
- 主键缺失、可空主键、未知类型、过长 VARCHAR 和重复列名。

结构检查不能替代校验和：修改一段仍满足布局的用户字节，解析器可能正常读出修改后的值。这是本阶段公开的边界，不应将这些测试宣传为全面损坏检测。

## 5. 本次实际执行的检查

2026-09-09，在 Go 1.25.7 darwin/arm64 上：

```sh
go test ./...
go test -race -coverprofile=/tmp/innodb-coverage.out ./...
go vet ./...
GOMAXPROCS=2 go test -run '^$' -fuzz '^FuzzRead$' -fuzztime=10s -parallel=2
```

全部通过；核心包语句覆盖率 94.4%。示例包没有单元覆盖，另通过实际 `go run` 核对输出。10 秒模糊测试执行 884997 次，无 panic 或不终止；这是本次运行量，不是每台机器必须达到的指标。

模糊测试在有效文件的一个根页内修改任意位置的一段字节，并验证解析能返回或报错。它没有证明所有字节组合、任意 schema 或其他格式安全。

MySQL 8.0.45 的 `innochecksum` 对全部七个原始夹具检查通过：

```sh
/Users/kimihiro/workspace/software/mysql-8.0.45/bin/innochecksum testdata/mysql8045/lesson_rows.ibd
```

成功退出码为 0，默认无输出。它验证交付夹具页校验和；Go 的读取路径当前不计算 CRC，两者不要混淆。

## 6. 重新生成样本

生成器需要运行中的 MySQL 8.0.45、16 KiB、file-per-table，以及能够创建专用数据库和持有导出锁的用户。示例命令沿用本机客户端和 socket 路径；在其他机器上替换它们。

下面从终端询问密码，只传给子进程环境，不写入文件或命令历史：

```sh
python3 - <<'PY'
import getpass
import os
import subprocess
env = os.environ.copy()
env['MYSQL_PWD'] = getpass.getpass('MySQL password: ')
subprocess.run([
    'python3', 'scripts/generate_fixtures.py',
    '--mysql', '/Users/kimihiro/workspace/software/mysql-8.0.45/bin/mysql',
    '--socket', '/Users/kimihiro/workspace/data/mysql/3306/data/mysqld_3306.sock',
    '--out', '/tmp/innodb-my-new-fixtures',
], env=env, check=True)
PY
```

输出目录必须不存在。生成器新建 `innodb_reader_fixture_<随机后缀>` 数据库，保留它以便复查；不会覆盖或删除旧库。输出打印新库名和目录。磁盘源数据文件需对执行脚本的本机用户可读。

当前交付夹具来自 `innodb_reader_fixture_5969d1f08e72`；此前五案例试跑库 `innodb_reader_fixture_c0ddccc26383` 也保留在本地，本项目未删除它。它们都是本阶段创建的测试库。

重建后直接用示例读取新目录中的 `.ibd`/`.json`。若要替换项目固定夹具，须同时更新预期结果、manifest、手册实测字节与位置测试；新快照的事务字段、LSN、ID 和哈希可能不同。失败产生的部分输出目录仅供诊断，不是成功快照，以完整 manifest 和检查结果为准。

## 7. 本阶段完成与下一阶段

我们已经从真实文件恢复了受支持表的每个列值，并能逐字节解释它们。程序仍要求可信 schema、新建插入型 DYNAMIC 表、唯一聚簇叶子页；它不自动恢复 SDI，不扫描多页 B+ 树，不处理 LOB 或事务历史。

下一步应扩展到多页、多层聚簇树的整表扫描，复用已经验证的叶子记录解码。验收重点应是不丢行、不重复、顺序正确；页统计或查询加速可以随后建立在这一能力上。
