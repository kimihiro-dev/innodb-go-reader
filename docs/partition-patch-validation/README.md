# 分区补丁兼容验收证据

2026-10-14按[分区目录说明](../format/94-partition-version-compatibility.md)实现5.7可信目录与8.0补丁SDI集合兼容。格式和限制见[第94章](../format/94-partition-version-compatibility.md)，实际统计/验收集中于[验证报告](report.json)和[report.json](report.json)。

## 原件与派生结果

- `testdata/partition-patches`：各准确版本新专用库在FOR EXPORT锁内取得物理文件、独立SQL预期、目录/字典、SHOW CREATE及生成SQL；5.7可信定义由独立Python SQL工具生成，8.0保留准确版本官方ibd2sdi。SHA256按解压文件计算。
- `source-audit.json`：15个本地源码目录的分区相关文件指纹与关键行号。source/版本与镜像/构建观察分别保存，不从源码相似推导测试通过。
- `server-*.json`：准确运行版本、镜像及本轮专用容器；没有密码。复用容器来自此前本聊天兼容测试，新5.7.44/8.0.45为独立基准。
- `official-57-tools.json`、`official-80-tools.json`：精简镜像缺失工具时从准确官方RPM提取，保留来源/SHA；8.0.30工具来源沿用[前轮记录](../mysql80-patch-validation/official-tools.json)。只增加验证工具，不改服务器。
- `physical-examples.json`：原始页字节/偏移与SDI观察；`cli-report.json`是Go驱动、manifest、CLI逐值导出及官方页校验结果。
- Go日志：集合逐值/二级回表、旧/新回归与失败路径；合成SDI/损坏副本不写回原件。`final-*.log`为最终复验，原列默认的复现/修正日志另存。`final-cli-report.json`记录最终修正后的受影响CLI；`builds.json`、`implementation.json`及`environment.json`提供构建/源码指纹与环境。`assets.json`核对本轮开始前全部既有固定文件，`lifecycle.json`确认专用容器已停止并保留数据。

## 重现

产品只使用已有标准库与MySQL驱动。Docker/mysql客户端/Python属于独立测试设施，产品schema采集在容器内运行时PATH为空，通过原生Unix socket连接。

```sh
go test ./... -count=1
go test -race . ./internal/mysql57 ./internal/cli \
  -run 'Test(Partition|Trusted|CollectPartition|GeneratePartition|Manifest|LegacyPartition)' -count=1
go vet ./...
go build -o /tmp/innodb-partition-reader ./cmd/innodb-reader
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o /tmp/innodb-partition-reader-linux ./cmd/innodb-reader
# 启动server-*.json对应专用容器后执行；默认重新跑，不读取已有通过结果。
python3 docs/partition-patch-validation/verify.py \
  --reader /tmp/innodb-partition-reader --linux-reader /tmp/innodb-partition-reader-linux
python3 scripts/check_docs.py
```

`--resume`仅用于保留本轮已完成版本的检查，报告/日志明确记录继续运行，不把历史缓存写作新执行。在线驱动只比较同一表最终DDL状态对应的快照定义；各早期快照由固定SQL响应回归覆盖，不能把EXCHANGE后的新身份套回先前文件。生成原件需使用[生成脚本](../../scripts/generate_partition_patch_fixtures.py)的`--container`和新的`--out`目录。

早期EXCHANGE及8.0.12首分区重建标为不支持，是实际厂商SDI身份/多对象边界，保留原件验证严格失败；子分区也是拒绝样本，页校验通过不等于格式受支持。结束后停止本轮使用的15台专用实例，保留数据用于复现；不改用户其他数据库、旧快照、源码标签或发布状态。
