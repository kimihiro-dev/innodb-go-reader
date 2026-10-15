# Schema驱动连接验收证据

本目录保存驱动连接的驱动直连验收；用法见[CLI指南](https://github.com/kimihiro-dev/innodb-go-reader-cli/blob/main/docs/CLI.md#schema采集可信定义)，连接与物理读取分工见[第91章](../format/91-cli-input-preparation.md)，选择理由见[驱动连接说明](../format/91-cli-input-preparation.md)。Docker/快照输入准备测试资料保留在原目录，不代表当前命令参数。

- [report.json](report.json)：实际环境、命令、结果、构建指纹及边界。
- [live-report.json](live-report.json)、[live.log](live.log)：三张真实5.7.44表、三种连接方式、失败路径的验收。
- [full-tests.log](full-tests.log)、[race.log](race.log)、[vet.log](vet.log)：本轮非缓存回归、受影响包race及全项目vet。
- [build-native.log](build-native.log)、[build-linux.log](build-linux.log)、[build-windows.log](build-windows.log)：构建诊断；成功时日志为空，命令及产物SHA256见报告。
- [original-assets.json](original-assets.json)：3247份既有夹具/验收资产的指纹核对结果。
- [verify-live.py](verify-live.py)：本轮实际执行脚本，路径针对专用临时测试环境；它不是CLI运行依赖。复验须准备测试实例、私有连接配置并调整临时路径，私有配置未纳入仓库。
- [lesson.sql](lesson.sql)：专用测试库的表DDL/DML；仅在独立测试实例运行。测试账号具有目标库SELECT及全局PROCESS权限，密码只保存在临时私有配置并通过环境传递给CLI。
- [ca.pem](ca.pem)、[server-cert.pem](server-cert.pem)：本次TLS实验的公开证书，不包含私钥。测试证书仅短期有效，复验需重新签发匹配DNS/IP SAN的证书。

live下保留每种连接的Schema/索引/原始来源与CLI执行状态；各reference目录保存独立SQL来源、预期单元格及外部采集的table.ibd.gz。Schema命令仅产出定义和来源，没有快照产物。模型按合法零值默认值归一化比较，原始SQL来源逐项一致；JSON数字保持整数精度，NULL不转成空值。

全部CLI调用的PATH为空，证明不需要Docker/mysql/Python可执行文件。测试基础设施仍使用Docker运行真实服务器；Unix socket是本机字节代理，后端为同一真实5.7.44实例，不称为macOS原生5.7服务器。外部测试辅助脚本在FOR EXPORT锁内复制快照，模拟调用方提供物理备份，锁不是Schema命令持有。

三种连接各采集普通DYNAMIC、隐藏聚簇COMPACT、STORED/VIRTUAL生成列三种表。九次采集与独立SQL元数据/模型匹配，九次导出的18行逐单元格匹配SQL预期，十二次二级检查通过；中文、引号、ENUM/SET、NULL、二进制、前缀键和隐藏定位均覆盖。认证错误、缺PROCESS权限、不受信任证书、缺表失败不发布输出；静默socket验证握手超时和SIGINT取消，均退出130且清理临时文件。密码不进入argv、日志或产物。

初始TLS握手失败：OpenSSL验证成功并协商DHE-RSA-AES256-GCM-SHA384，显式AES128-GCM-SHA256也可用；Go默认套件没有交集。仅用于定位的GODEBUG=tlsrsakex=1证实RSA密钥交换原因。最终实现直接列出TLS 1.2 ECDHE/AES-GCM及RSA/AES-GCM，成功连接不依赖GODEBUG，证书链和主机名仍严格校验。RSA密钥交换不具备前向保密性，未启用CBC/3DES或明文回退。超时实验曾把预期退出码写成1，核对现有CLI契约后改为130；该项为验证脚本修正。

44组固定定义和七组分区清单回归仍由Go测试覆盖。早期A缺少内部列计数的一项响应合成补充，B使用独立SQL计数；真实驱动连接不扩大版本、分区或恢复范围。Linux/Windows仅交叉编译，没有声明目标系统运行验收。原始资产保持，未提交、推送或发布。
