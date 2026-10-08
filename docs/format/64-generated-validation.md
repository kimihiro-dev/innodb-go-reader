# 64 仅物化列接口、NULL位图与验证

学习目标：正确使用严格/仅物化入口，理解VIRTUAL为何不占NULL位，并复跑真实SQL与官方工具验证。

前置：[63 生成列的实际存储](63-generated-column-layout.md)、[62 INSTANT验证](62-instant-validation.md)。

## 1. 两种读取契约

| 入口 | 含VIRTUAL表 | 成功结果 |
|---|---|---|
| Read / ReadAuto | ErrUnsupported，nil结果 | 完整当前用户列，包含INVISIBLE与STORED |
| ReadMaterialized / ReadMaterializedAuto | 物理布局受支持时可读 | Columns、VirtualColumns、Result |

仅物化入口不执行任意列投影：它读取**全部物化用户列**。`result.Result.Records[i].Values[j]`对应 `result.Columns[j]`；TextBytes、CharStorage、EnumIndexes、SetMasks、External.Column和DefaultColumns中的列号也按同一Columns解释。不存在VIRTUAL占位nil。

`VirtualColumns`包含Name、从1起的用户列Ordinal、Expression和Invisible；它表示值尚未物化，即使SQL可以计算，解析器也没有返回该值。`Column.GenerationExpression`描述STORED来源，`Column.Invisible`描述用户不可见性。若需要原始DD类型/字符属性，可查看 `InspectTable.Columns`。

```go
view, err := innodb.ReadMaterializedAuto(file, fileSize)
if err != nil {
    return err
}
for _, row := range view.Result.Records {
    for j, value := range row.Values {
        fmt.Printf("%s=%v\n", view.Columns[j].Name, value)
    }
}
for _, column := range view.VirtualColumns {
    fmt.Printf("%s: 未物化，SQL列序号%d\n", column.Name, column.Ordinal)
}
```

显式入口 `ReadMaterialized(file,size,schema)` 使用可信schema：Columns必须完整描述物化列，VirtualColumns另列。它不会反查SDI来纠正用户遗漏；这与原Read的可信输入前提相同。调用方不能删除VirtualColumns后把输出宣称为完整SQL行。

元数据仍遵守原约定：Issues非空时Schema=nil。若唯一问题是用户VIRTUAL未物化，MaterializedSchema可用且保留VirtualColumns；任何其他未支持特性都使MaterializedSchema也为nil。仅物化入口不会通过过滤错误字符串来绕过检查。文件损坏、LOB失败或I/O失败仍返回nil，无部分结果。

## 2. VIRTUAL不增加NULL位图

真实tree表有id、padding、九个VIRTUAL INT、一个可空STORED copy和八个可空普通INT。物化可空列数是9，不是18：

```text
SQL可空声明：VIRTUAL×9 + STORED copy + n0..n7
聚簇NULL位：              copy(bit0)  n0..n7(bit1..8)
字节数：ceil(9/8)=2
```

`tree_initial`和`tree_instant`各有102个聚簇页，根level1。后者第一条非叶子记录在页4，Start120、origin127、End135，真实字节：

```text
00 00 | 10 00 11 00 0f | 80 00 00 00 | 00 00 00 05
NULL预留    MIN_REC固定头      存储键0       子页5
```

这两个NULL字节位于页内[120,122)，固定头[122,127)，键[127,131)，子页号[131,135)。键与子页为大端，记录头沿用前章规则。不能计入VIRTUAL而错误消费第三个NULL字节。

首个叶子记录origin131，Start120，头前数据：

```text
b0 84 b0 84 | 01 fe | 00 00 10 09 7c
两个长度1200  NULL位      固定头
```

长度从右往左解释，84b0代表1200字节；NULL字节向前消费，fe与01对应bit1..8全部为NULL，而copy非NULL。两个实际长度分别属于padding和STORED copy。

INSTANT在最前ADD可空added后，旧行由DefaultColumns=[0]补88；新行有版本字节。非叶子仍按版本0的9个物化可空列保留两字节，不将后来添加列或VIRTUAL错误加入初始位图。

## 3. 二级索引里可能存有虚拟值

`indexed_initial`具有用户VIRTUAL v及其普通BTREE索引iv；InspectTable报告并核对两个索引根，仅物化输出仍是id/a。即使二级索引保存了某些表达式结果，本阶段也不解析它、不用它给聚簇行补VIRTUAL值，不承诺SQL表达式执行。

函数索引 `KEY fx((a+1))` 的内部hidden=3列与显式用户VIRTUAL不同；真实 `functional_rejected` 由自动完整/仅物化入口同时拒绝。系统hidden=2、已DROP物理列和用户hidden=4也分别处理。

## 4. 真实快照矩阵

来源为独立临时MySQL8.0.45库 `innodb_reader_generated_d64ef4eedc8a`，仅Unix socket、禁用网络；不是原用户实例。先前原实例因缺凭证无法连接，未尝试改其凭证或数据。临时实例在验证完成后关闭。

| 快照组 | 份数 | 成功SQL行数 | 重点 |
|---|---:|---:|---|
| stored_values | 2 | 6 | 数字/UTF-8生成值、NULL、基础列更新、INVISIBLE转VISIBLE |
| mixed | 5 | 16 | 不可见主键/生成列、两个VIRTUAL、两个LOB、INSTANT增删/重建、移除VIRTUAL后完整读取 |
| tree | 2 | 1201 | 九个VIRTUAL、102页树、初始/INSTANT NULL位图 |
| hidden | 1 | 3 | 隐藏ROW_ID与重复用户行，多重集合对照 |
| unique_stored | 1 | 3 | 实际STORED唯一非空聚簇键 |
| empty_values | 1 | 0 | 空表也保留未物化说明，严格入口仍拒绝 |
| indexed | 1 | 3 | VIRTUAL二级索引共存，聚簇物化值读取 |
| functional | 1 | 不计成功 | 内部隐藏生成列的明确拒绝 |
| 合计 | 14 | 1232 | 13份成功、1份拒绝 |

每份文件都有SHA256、DDL、SQL指定列预期、SQL索引身份和官方SDI。显式schema类型来自生成器声明，表达式的规范文本及INSTANT物理映射来自官方SDI的独立Python序列化；不能将两份schema一致称为两个独立元数据来源。独立正确性依据为SQL值、物理字节、完整树与损坏测试。物化列中的不可见列明确指定查询；二进制用HEX，数值保留精确JSON。隐藏键表用多重集合保留重复次数。

生成器在每次DDL/DML结束后持有FOR EXPORT锁，锁内复制文件并查询预期，最后UNLOCK；完整执行语句在writer.sql.gz。保留只用于诊断的临时失败采集，不把它们计入正式14份资产。

## 5. 离线复现与损坏测试

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go test -run '^$' -fuzz '^FuzzMaterializedRead$' -fuzztime=10s -parallel=2
python3 scripts/verify_generated_fixtures.py --mysql-bin /Users/kimihiro/workspace/software/mysql-8.0.45/bin
```

独立官方验证覆盖14个文件严格crc32、28个SDI对象以及完整/仅物化CLI行为；仅物化示例 `go run ./examples/materialized table.ibd` 输出包含Columns、VirtualColumns及Result的JSON对象。输入应先解压ibd.gz，解压仍属于调用方职责。

`generated_test.go` 默认离线验证：真实SQL逐值、无序多重集合、列序/不可见/表达式元数据、自动/显式完整结果、原始SHA、官方SDI、SQL索引身份和失败无部分返回。损坏SDI测试覆盖缺表达式、内部/未知隐藏类型、VIRTUAL附带physical_pos或default、错误物化分类、错误键/表身份及其他不支持物化类型。显式schema测试覆盖重复名称、与物化列冲突、缺表达式、错误Ordinal、VIRTUAL键和超限列数；故意破坏实际LOB检查两条入口都没有部分结果。

首轮全量race/coverage通过，核心覆盖率94.3%，vet通过；FuzzMaterializedRead十秒预算86,663次执行无失败。新测试追加的元数据分类和旧schema兼容检查另有定向验证。累计425份资产，423份在对应完整或仅物化入口成功读取79,744行，另有既有LOB超限和新增函数索引两份拒绝样本；此累计包含仅物化行，不等于每张表全部SQL列均已恢复。

## 6. 边界

保持8.0.45/16KiB/DYNAMIC/非压缩非加密独立表空间及已验收的类型、键、LOB和原生INSTANT范围。STORED沿用这些类型的解码能力，不增加任意函数、类型或collation。样本中的VIRTUAL文本使用未纳入物化文本矩阵的collation也可报告，因为本阶段没有解码其值。

不实现表达式求值、二级索引物化值恢复、函数索引、任意投影、旧式INSTANT、SQL所有DDL组合、事务可见性重建或在线文件一致性判定。已DROP生成列及升级混合布局仍沿用明确拒绝。STORED结果不校验是否与当前表达式重新计算一致，快照可靠性仍由调用方保证。
