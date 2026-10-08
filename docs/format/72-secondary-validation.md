# 72 二级索引整树接口与独立验收

本章目标：安全使用独立二级记录模型，区分当前二级项与删除标记，并用SQL、官方工具和损坏测试验证整树结果。字段排列、NULL、前缀与真实字节见[第71章](71-secondary-record-layout.md)。

## 一、完整读取与同步扫描

```go
// 按SDI中的准确索引名选一棵二级树。
result, err := innodb.ReadSecondaryAuto(file, size, "n_idx")
if err != nil {
    // result为nil，完整读取不返回部分结果。
    return err
}
for _, record := range result.Records {
    // record.Values按照result.Schema.Fields排列；不是表的完整列序。
    // record.ClusteredKey提供聚簇定位值，尚未自动回表。
}
```

需要可信显式布局时，先调用InspectSecondary取得并审核SecondarySchema，或由调用方按契约构造，然后传给ReadSecondary。RootPage/IndexID/SpaceID必须与文件一致；错误的结构或排序不会被当作有效记录跳过。完整结果包含Schema、根Page、所有访问Pages、Nodes、Records和DeletedRecords。

同步扫描提供ScanSecondary与ScanSecondaryAuto，使用context、ScanOptions和独立SecondaryEvent：

```go
report, err := innodb.ScanSecondaryAuto(ctx, file, size, "n_idx",
    innodb.ScanOptions{CachePages: 32},
    func(event innodb.SecondaryEvent) error {
        switch {
        case event.Record != nil:
            // 消费当前二级项。
        case event.DeletedRecord != nil:
            // 消费独立删除标记证据。
        }
        return nil
    })
if err != nil || !report.Complete {
    // 已交付的前缀无法撤回，不可称为完整索引扫描。
}
```

每次事件只含一个Page、Node、Record或DeletedRecord。页先于其项交付，DFS顺序与物理键序一致，事件数据可独立保留/修改。调用方仍须保持schema和底层快照不变；context不能强制中断任意阻塞ReaderAt或回调，不自动关闭文件。

SecondaryScanReport中的计数包含返回错误的那次回调。Complete只有在整棵选定二级树及各层链尾核验结束后才为true；ErrStopped、ErrLimit、取消、I/O与结构错误都使它为false。PageReads包括命中和Auto元数据请求，PhysicalReads只计实际读取；不能把这些数字等同于结果中的二级页数。

MaxRows计当前二级记录，删除标记不占它；MaxRowBytes限制当前记录本地源字节，二级路径没有LOB物化。MaxEntries累计遍历任务/排队项和解码页项，页缓存与预算沿用[第68章](68-streaming-lob-and-budgets.md)。ReadSecondary保留旧完整读取风格，不自动套用Scan默认预算。

非索引VIRTUAL列不影响物化索引项的解释，InspectSecondary基于既有MaterializedSchema核对来源；若所选索引本身引用VIRTUAL仍拒绝。并不由此放宽函数索引、未知表布局或其他既有元数据限制。

## 二、真实矩阵

12份新快照来自MySQL8.0.45隔离临时库 `innodb_reader_secondary_12402f8b6069`。共有5249条当前聚簇行，选定20棵二级树，合计8150条当前二级项、60条delete-mark、216个二级页。

| 表 | 当前表行 | 二级树数 | 主要验证内容 |
|---|---:|---:|---|
| tiny | 181 | 1 | TINYINT、NULL、重复值和7字节记录 |
| empty | 0 | 1 | 空根叶页正常完成 |
| integer | 2500 | 2 | INT DESC、可空唯一BIGINT UNSIGNED、64位高值、多页 |
| deep | 2000 | 1 | utf8mb4长字符、混合DESC/ASC、NULL、182页三层树 |
| overlap | 120 | 2 | 同列前缀加完整定位副本、完整重叠字段反向复用 |
| wide_prefix | 10 | 1 | NULL/空串及127/128/199/200/201/300/400字节输入 |
| compact | 80 | 2 | 多字节CHAR前缀、latin1 CHAR空格、VARBINARY/BINARY前缀 |
| rowid | 5 | 2 | 隐藏ROW_ID、NULL和重复用户行 |
| unique_cluster | 100 | 2 | 无显式主键的唯一非空聚簇身份、可空唯一二级 |
| generated | 160 | 1 | STORED INVISIBLE索引、非索引VIRTUAL、INSTANT ADD |
| scalars | 3 | 3 | 精确DECIMAL、日期/年、BIT、时间负小数和UTC TIMESTAMP |
| changes | 90 | 2 | 修改二级值、删除行、保留各30条旧二级项 |

每棵树保存明确的FORCE INDEX和ORDER BY SQL，逐项比对物理字段投影与有序结果。复合方向逐成员写出；二进制使用HEX；时间/DECIMAL保存精确字符串；测试以UseNumber读取JSON，避免64位数经float64失真。整表物化值另存SQL预期，按多重集合验证，保留重复次数。

聚簇定位键还要能解析到已经通过SQL验证的聚簇结果。这个检查覆盖字段映射和ROW_ID，但不冒充SQL可直接读取隐藏ROW_ID。rowid表的独立SQL只验证可见二级字段与重复次数；隐藏定位值单独与物理聚簇身份核对。

## 三、delete-mark不是SQL当前行

changes初始插入id=0..99，n=id%7，k为带三位编号的字符串。一个独立全局一致性读视图保留旧版本；写会话修改id<20的n和k，再删除id>=90，提交后采集FOR EXPORT快照。读视图不在目标表上持有元数据锁，避免阻塞导出。采集后提交保持会话并正常关闭临时实例。

每棵二级树输出90条当前项和30条删除项：前20条旧索引值加最后10条被删行。删除键集合根据已保存INSERT/UPDATE/DELETE语句独立计算并逐条核对，既不把这些记录混入SQL当前结果，也不声称从它们恢复事务可见性。全物理键序和页记录计数仍包含这些在链删除项；PAGE_FREE残留不作为记录输出。

真实changes.n_idx删除项位于页5、origin=126、Start=120、End=134，原字节为 `00 20 00 10 00 62 80 00 00 00 80 00 00 00`。首字节00是NULL位图，头首字节20的delete-mark位已置位，载荷为旧n=0、id=0。它与当前值n=100分开输出。

二级记录没有逐行DB_TRX_ID和DB_ROLL_PTR，不能把聚簇DeletedRecord的事务字段照搬过来填零。SecondaryRecord的Raw/Header/DeleteMarked保存实际证据。发生未支持标志、键序重复或未知布局时仍报错，不用delete-mark绕过解码检查。

## 四、分层验证与损坏注入

`TestSecondarySQL` 对照全部真实矩阵，核验原始SHA、整表SQL、二级SQL、显式/Auto与完整/扫描一致性、定位映射及删除集合。`TestSecondaryExisting` 复用第26阶段已交付资产，包含DESC聚簇后缀和可空唯一键，原文件不改。

`TestSecondaryFailures` 在内存副本上注入CRC失配、错误索引身份、根兄弟链接、层级、零子页/循环、叶链、记录status/info和目录损坏。结构测试重新封装CRC，避免只测到外层校验失败；专门CRC测试不重封装。完整入口始终错误返回nil。

另测完整物理键重复与非NULL唯一用户键重复，区别于合法的“二级值相同、聚簇定位不同”。schema测试覆盖前缀超界、非法类型/字符集、可空或重复定位字段、错误ROW_ID、索引名与布局。流式验证回调错误计数、主动停止、上下文取消、页/行/字节/遍历预算、中途I/O失败，以及修改每个事件后的遍历稳定性。

12文件全部通过官方innochecksum严格CRC32、ibd2sdi逐对象对照与Go SDI/二级CLI核验。`verification.json`记录工具路径和逐文件结论，`physical.json`记录Go派生结构统计及真实样本偏移。派生统计不是独立SQL基准。

10秒预算FuzzSecondaryPage完成395387次执行，无失败。它在真实tiny根页副本上变异字节、重封装CRC并检查解析不崩溃、错误无部分返回；不是历史版本兼容证明。全量race/coverage和vet通过，核心覆盖率93.8%；补充删除键集合的定向race也通过。

## 五、复跑和边界

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go test -run '^TestSecondary' -v .
go test -run '^$' -fuzz '^FuzzSecondaryPage$' -fuzztime=10s .
python3 scripts/verify_secondary_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin
go run ./examples/secondary /path/to/uncompressed.ibd n_idx
```

Go测试与官方验证均离线读取保存的快照，不需要运行MySQL。CLI只在完整读取成功后输出SecondaryResult JSON，失败写stderr并返回非零状态。二进制Values/FieldBytes/Raw按Go标准JSON序列化为Base64；独立SQL使用HEX，两者在验证脚本中明确转换。

重新采集使用 `scripts/generate_secondary_fixtures.py --help`，要求新输出目录并创建独立新库；这属于写入测试数据的采集流程，不是普通离线回归。现有用户表、原实例与历史资产保持不变。

新增资产纳入全量聚簇Read/Scan/Query回归后，共484份资产，482份成功恢复95331行，2份既有类型/布局拒绝保持。二级接口只认证选定索引树，不保证其他二级树、整表事务一致性或当前SQL可见性。第35阶段的二级点查/范围、回表、投影和覆盖查询尚未开始。
