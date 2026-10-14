# MySQL 5.7明确补丁验收证据

本目录保存六个5.7补丁的源码、官方镜像来源、驱动与CLI验证。范围和教学解释见[第92章](../format/92-mysql57-patch-compatibility.md)，全部独立SQL/物理文件见[夹具入口](../../testdata/mysql57-patches/README.md)，验收摘要见[验证报告](report.json)。

- [report.json](report.json)：最终实际检查、来源及边界，构建SHA256。
- [source-report.json](source-report.json)、[format-summary.json](format-summary.json)、[bytes.json](bytes.json)：只读源码文件SHA/定义行号、JSON格式说明一致性及固定字节。source-diffs为各候选相对5.7.44的完整文件差异，不表示所有差异都影响当前解析。
- [patch-tests.log](patch-tests.log)、[full-tests.log](full-tests.log)、[race.log](race.log)、[vet.log](vet.log)：独立补丁SQL核对、完整回归、相关包race及全项目vet。
- [cli-report.json](cli-report.json)、[cli-matrix.log](cli-matrix.log)：264次驱动Schema采集、metadata/export读取、228次二级检查、六次原生Unix socket与TLS能力。
- [rejections-report.json](rejections-report.json)、[rejections.log](rejections.log)：六补丁各七例真实不支持输入，确认错误和失败清理。
- [tls-capabilities.json](tls-capabilities.json)、[tls-capabilities.log](tls-capabilities.log)：各镜像用自身客户端获取TLS协议/套件，和产品TLS结果分别标识。早期have_openssl变量不能区分其构建库。
- images下保留旧schema1原始manifest、层摘要/解压diff ID、转换说明和本地镜像ID；后期镜像摘要在对应pull日志中。旧镜像只转换封装，未重编译服务器二进制，源码核查不称为源码构建验收。
- server版本JSON记录最终运行版本/镜像ID/本机端口，capture日志记录独立SQL工具实际过程。cli和reject版本目录保存定义/来源及stdout/stderr原件；较大的stdout仅无损gzip保存。
- [setup-servers.py](setup-servers.py)、[capture-matrix.py](capture-matrix.py)、[verify-cli.py](verify-cli.py)、[verify-rejections.py](verify-rejections.py)、[diagnose-tls.py](diagnose-tls.py)、[convert-schema1.py](convert-schema1.py)：本轮测试工具，路径针对临时环境；不是产品运行依赖。

复验需准备新的独立实例、输出目录、匹配证书及临时私有账号配置，并按实际路径调整脚本。测试账号只用于专用实例，密码经环境/stdin传递，没有保存到公开证据；证书仅保存CA及服务器公开部分，不含私钥。原生socket验证将静态Linux产品二进制放入对应服务器容器，使用真实Unix socket及空PATH；macOS TCP采集也使用空PATH。运行时不需要外部客户端。

独立预期由既有Python转换/SQL生成器取得；生产Schema由Go驱动单独采集，模型按合法零值默认值归一化，原始SQL来源逐项对照。Go补丁测试逐值核对Read/Scan/LOB流、聚簇及二级查询；CLI另确认生成定义可消费、完整退出及协议行数，CLI导出不另宣称逐单元格序列化认证。

早期镜像初始化曾使5.7.12采集连接遇到临时服务器关闭，相关[服务器日志](server-5.7.12-failure.log)保留。最终改为TCP就绪后采集；早期初始化也会清理账号，专用账号在最终服务上重新配置。容器随机端口会随重启重新分配，驱动验证使用最终inspect所得端口。yaSSL需传统RSA私钥编码，最终服务have_ssl=YES。以上是测试设施修正，没有放宽产品认证/TLS策略。

新增夹具使递归空间测试资产数由531增为795；初次完整回归仅因旧计数失败，[原日志](initial-full-tests.log)保留。测试计数同步扩大，不跳过新资产；其他原首版自动SDI矩阵仍按原范围执行。旧夹具及验收文件指纹保持，不移动v0.1.0标签，不提交/推送/发布。
