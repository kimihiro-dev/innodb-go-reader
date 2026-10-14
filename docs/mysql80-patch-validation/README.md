# MySQL 8.0补丁兼容验收证据

2026-10-14按[8.0版本说明](../format/93-mysql80-patch-compatibility.md)核对用户提供的准确源码，并使用独立官方实例验证8.0.11/12/16/19/23/28/30。实际结果只在[验证报告](report.json)与[report.json](report.json)汇总，格式解释见[第93章](../format/93-mysql80-patch-compatibility.md)。

## 证据分类

- `source-versions.json`：本地准确源码文件指纹、DD/SDI及布局依据行号；不由运行镜像冒充源码差异。
- `server-*.json`：VERSION()、官方镜像ID/摘要及本轮专用容器名，无密码或用户实例内容。
- `official-tools.json`：8.0.30镜像的精简服务器包缺少工具，从同版本官方完整RPM只提取ibd2sdi/innochecksum，保留下载来源与SHA256，服务器二进制不改动。
- `physical-examples.json`：新固定文件中的字节、偏移、私有属性，属于直接物理观察。
- `testdata/mysql80-patches/<version>`：独立FOR EXPORT快照、生成/SHOW CREATE SQL、逐值SQL预期、官方SDI及原始来源；gzip文件SHA按解压后的物理文件计算。
- `cli-report.json`：217份新文件原生CLI及准确版本innochecksum校验；`cli-sql-report.json`补充CLI无损单元格与SQL预期对照。页校验不代替列值认证。
- Go测试日志：原有8.0.45/5.7回归与新补丁测试、合成失败、取消/预算、race及vet。合成CRC重封装副本不称为真实服务器输出。
- `assets.json`：本轮开始前的全部既有固定资产逐项指纹核对结果；只添加新资产，不改写旧快照或验收原件。

## 重现

产品构建使用go.mod/go.sum已有依赖，无新增依赖。`setup-servers.py`只启动本轮七个专用测试实例，不复用用户库；所有镜像为官方linux/amd64。准确版本须先下载。8.0.30工具补齐方法及哈希见official-tools.json；setup脚本不安装它们。

```sh
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -o /tmp/innodb-80-reader ./cmd/innodb-reader
python3 docs/mysql80-patch-validation/verify-cli.py --reader /tmp/innodb-80-reader
# 重新启动server-*.json对应专用实例后，可加 --official 复核页校验。
python3 scripts/check_docs.py
```

重新采集时使用生成脚本的`--container`和新的`--out`目录，脚本拒绝已有输出目录。首轮空间值预期使用默认轴序曾与物理轴序不同，最终采集统一显式`axis-order=long-lat`并在七个服务器独立核对，没有由Go结果修改SQL预期。原型失败快照在/tmp，仓库仅保存最终独立采集。

本轮结束停止自己创建的七个容器，保留数据便于复现；不修改用户其他容器、MySQL源码、v0.1.0标签或发布状态。新补丁单表实测不证明分区集合、升级混合布局或恢复能力。
