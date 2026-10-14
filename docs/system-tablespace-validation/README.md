# 系统表空间教材的独立证据

本目录服务[第89章：5.7.44](../format/89-mysql57-system-tablespace.md)与[第90章：8.0.45](../format/90-mysql80-system-tablespace.md)。它是教材固定字节和来源记录，不是系统空间解析器或恢复验收，也不加入既有testdata能力计数。保存的文件子集不是可直接启动的完整实例备份。

## 来源与采集

5.7.44在新建的linux/amd64隔离Docker实例运行，使用已固定的官方镜像digest，无网络与端口；8.0.45使用本机官方发行二进制，新建临时datadir，仅Unix socket，关闭TCP与X Plugin监听。只写入专用lesson库，创建space0的shared_rows和独立private_rows，记录SQL、INFORMATION_SCHEMA物理身份及环境。此前项目测试容器不参与此采集。

两实例先设置innodb_fast_shutdown=0，完成干净关闭，再复制系统/用户文件；8.0另复制mysql.ibd及默认undo文件。版本、页大小、checksum、配置、文件原路径、长度和解压SHA256在各版本source.json中；会话SQL、关闭日志、原始工具命令与结果一并保存。

capture.py是本次采集脚本原件，包含本机路径、唯一实例名与“输出目录须不存在”的保护。它用于说明如何产生证据，不是可直接覆盖本目录重跑的通用工具；创建新样本须更换输出位置与实例名。首次Docker mysqladmin客户端随容器主进程退出而中断，最终采集改以容器exit0和Shutdown complete日志确认干净关闭；首轮材料留在临时目录，未伪称其成功采集。

## 文件职责

| 文件 | 内容与性质 |
|---|---|
| 5.7.44 / 8.0.45目录中的*.gz | 原始物理文件或官方SDI提取的无损压缩副本 |
| VERSION/source.json | 实际配置、SQL表/索引身份、已知行预期、原始物理文件SHA256 |
| VERSION/session.sql | 创建、写入、查询和关闭前设置语句 |
| observations.json | inspect.py派生的固定偏移观察；不是独立SQL预期 |
| source-map.json | 只读核查的两个官方源码包路径、版本与文件指纹 |
| tools.json | 官方8.0.45 innochecksum/ibd2sdi实际退出及输出 |
| mysql57-shutdown.log / mysql80-shutdown.log | 干净关闭证明和实际启动环境 |
| mysql80-files.json | 8.0双写/redo/undo等实际文件名与大小观察；未保存外部双写/redo全体原件 |
| capture-log.json / host.json | 本轮具体命令、结果与独立临时实例位置 |
| inspect.py | 只读复验教材涉及的固定字段，不解码全字典、不执行恢复 |
| report.json | 执行边界与结果索引 |

source.json里的SQL索引身份与原始页面ID/root核对属于独立来源比较；SYS字典页compact位、旧双写头和undo数组来自固定原件字节。通过源码解释字段不等于已实现完整记录解码。

## 只读复验

从仓库根目录执行：

```sh
python3 docs/system-tablespace-validation/inspect.py
python3 scripts/check_docs.py
```

inspect.py逐文件核对解压长度/SHA256，并验证教材中的系统入口字段、SQL根身份、8.0 SDI定位和undo目录。它会刷新派生observations.json，不改原始gzip、SQL或源码。源码指纹可按source-map.json与本机所提供目录复核。

下面复验第89章SYS_INDEXES根地址和根页索引ID；仅适用于本目录5.7样本：

```python
import gzip

with gzip.open('docs/system-tablespace-validation/5.7.44/ibdata1.gz', 'rb') as f:
    f.seek(7 * 16384 + 82)
    root = int.from_bytes(f.read(4), 'big')
    f.seek(root * 16384 + 66)
    index_id = int.from_bytes(f.read(8), 'big')
    print(root, index_id)  # 固定原件输出：11 3
```

## 工具和结论的边界

innochecksum明确是8.0.45官方工具，对两个版本的新文件进行严格crc32核对。其实现会对系统空间旧双写区域采用跳过规则，因此“退出0”不等于所有暂存副本均经相同页身份/CRC认证；更不认证事务可见性、字典列值或完整恢复。

ibd2sdi对8.0三种空间提取的JSON按原样gzip保存：普通独立表、系统空间中的shared_rows，以及mysql内部表定义。它证明官方SDI提取能力，不证明Go的单表入口支持space0或mysql字典记录。

本例8.0 undo目录size原文为0，但有128个非空回滚段指针；代码参考与实际槽观察分别说明。本例旧双写头仍保留而活跃双写文件独立存在。legacy页类型落盘为13的解释基于创建/刷盘源码推断，未把未启用的逐页校正日志写成实际跟踪结果。

本次没有MVCC、undo/redo应用、change-buffer应用、损坏修复、加密或升级实例恢复实验。未进行Go解析实现变更，既有支持矩阵及原始夹具保持原范围。
