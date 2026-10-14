# CLI输入准备验收证据

本目录保存输入准备的Go采集、完整分区清单生成与失败契约验证。功能入口与前提见[CLI指南](../CLI.md#schema采集可信定义)，格式与字节说明见[第91章](../format/91-cli-input-preparation.md)，验收摘要见[验证报告](report.json)。

- [report.json](report.json)：最终检查、范围和限制。
- [full-tests.log](full-tests.log)、[race.log](race.log)、[vet.log](vet.log)：本轮非缓存Go回归、受影响包race及全项目vet原始结果。
- [live-report.json](live-report.json)、[live.log](live.log)：真实5.7.44 Docker实例六种表的采集/读取及分区生成结果。
- [verify-live.py](verify-live.py)：本轮执行的验证脚本，仅用于复验，不是CLI运行依赖；其中路径/容器针对本轮专用测试环境。
- live下按案例保存Go/Python的schema、indexes、原始SQL来源、Go快照gzip及执行状态。物理SHA256对解压后的文件计算，保存在Go source.json中。
- [manifest-example.json](manifest-example.json)保存生成器的实际输出，路径相对本轮临时清单目录，仅作为格式证据，不能脱离对应临时文件直接运行；固定原件仍在testdata/partitions。

固定单元测试比较Schema/SecondarySchema模型与既有SQL采集证据。早期A来源缺少内部列计数，该一项响应在测试中合成补齐；B来源保存了独立SQL计数。Go可能明确输出合法的零值属性，实时对照按相同模型默认值归一化，保留uint64和SQL NULL，不宣称JSON字节完全相同。

真实导出验证最终协议和独立预期行数，未在本轮新增逐单元格SQL认证；完整列值仍由已有固定回归覆盖。manifest在乱序/重命名输入下按SDI取得名称，随后复用InspectPartitions完整预检，并用生成清单执行metadata/check/export。失败/取消/预算、损坏、不完整集合、输出保护及客户端协议另由Go测试覆盖；实时实验还验证不存在的表和stdout断开时清理新目录。

本轮不会移动v0.1.0标签，也不修改既有固定资产。跨平台编译只证明能构建，不能替代目标平台运行；新增采集不提供.frm/.cfg/系统字典离线发现、5.7分区、MVCC或恢复。
