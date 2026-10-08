# 65 COMPACT 行布局与两种页外格式

本章目标：从行格式、记录长度和实际页类型三个层次，判断一个长字段如何还原。先读[页外引用](13-lob-reference-and-pages.md)、[更新后的 LOB](59-updated-lob-layout.md)及[手册目录](README.md)中的 INSTANT/生成列章节；本章延续当前物理值契约，不执行历史 MVCC 或 VIRTUAL 表达式。

## 一、行格式与 LOB 格式分别判断

COMPACT 和 DYNAMIC 都使用 compact 记录头、NULL 位图及逆向变长长度信息，树导航无需另写一套。不同点在页外字段的本地部分：

| 行格式 | 本地页外字段 | 引用中的 Length |
|---|---|---|
| DYNAMIC | 20 字节引用 | 全部页外字节 |
| COMPACT | 768 字节前缀 + 20 字节引用 | 仅后缀，不含前缀 |

不是每个超过 768 字节的值都会页外存储。InnoDB 根据整条记录是否过大选择字段外置；页内字段仍按记录的实际长度读取，不强行拆分。

实测普通 MySQL 8.0.45 在 COMPACT 表中写出的是新 LOB_FIRST/LOB_DATA。`lob0lob.h:228–240` 的 `is_big()` 固定返回 true，`lob0impl.cc:944–952` 的小值旧写入路径不因 COMPACT 自动启用。官方 debug 二进制的 `lob_insert_noindex` 调试点可强制旧链写入。两类样本独立保存，不能据后者宣称普通版本默认生成旧链。

```text
记录长度包含 external 标志
  ├─ DYNAMIC：引用 20
  └─ COMPACT：前缀 768 | 引用 20
                          ↓ FirstPage
                 实际 FIL_PAGE_TYPE
                   ├─ 24：新 LOB，引用第三字段是 Version
                   └─ 10：旧 BLOB，第三字段是 HeaderOffset=38
                          ↓
                 页外后缀拼接完毕
                          ↓
                   前缀 + 后缀 → 类型解码
```

源码依据为本地官方 8.0.45：`storage/innobase/data/data0data.cc:430–448` 的 local_len；`storage/innobase/lob/lob0impl.cc:1121` 附近按实际页类型分派的读取路径；`storage/innobase/lob/lob0lob.cc:461` 的 debug 写入点。版本固定，不能把另一版本的常量直接套用。

## 二、SDI、空间标志和记录头共同约束

自动入口从 DD `row_format=5` 得到 COMPACT，`2` 得到 DYNAMIC，枚举见 `sql/dd/types/table.h:80–87`。`Schema.RowFormat` 省略继续表示 DYNAMIC，显式 COMPACT 必须与文件空间标志吻合。

页 0 偏移 54 的四字节 FSP flags，在本阶段 COMPACT 教学样本是 `00 00 40 00`；与 `0x21` 掩码相与为 0。DYNAMIC 的相应掩码必须为 `0x21`。该检查仍保留既有页大小、独立表空间、压缩与加密限制。REDUNDANT 也可能具有非 atomic 空间标志，因此不能仅凭 flags 接受用户记录；ReadSDI 可提取原始字典，InspectTable 仍检查 DD 格式，索引页还要满足 compact 记录标志。

COMPACT 完整聚簇键每个成员最多 767 字节，总上限仍为 3072。实际配对夹具使用 `VARBINARY(767) DESC, INT`，总宽 771 合法；测试把单个成员改为 768 时拒绝。这里是物理编码上限，不是字符数上限。

## 三、逐字节读取真实 COMPACT 引用

解压 `testdata/compact-legacy/lesson_compact_initial.ibd.gz`。第一条记录的 txt 引用在页 4 的偏移 921，绝对文件位置为 `4×16384+921=66457`。其本地前缀从页内 153 开始，恰好 768 字节；引用如下：

```text
00 00 00 02 | 00 00 00 05 | 00 00 00 26 | 00 00 00 00 | 00 01 0e 70
space=2       first=5       header=38      flags/high     suffix=69232
```

完整 txt 为 70000 字节：`768+69232`。不要把 Length 当完整长度，否则会遗漏前缀或错误地拒绝正确链。

同一逻辑数据的普通 COMPACT 样本 `testdata/compact/lesson_compact_initial.ibd.gz` 也在页 4 偏移 921：

```text
00 00 00 1d | 00 00 00 05 | 00 00 00 01 | 00 00 00 00 | 00 01 0e 70
space=29      first=5       version=1      flags/high     suffix=69232
```

引用第三字段不能单独用于猜格式。先读首个页并检查 CRC、页号、space/type，再解释该字段；旧 BLOB 要求值 38，新 LOB 继续使用版本与活动/历史项检查。

## 四、前缀可以切断字符

两个样本的前缀最后 12 字节都是：

```text
f0 9f 98 80 e7 95 8c f0 9f 98 80 e7
```

结尾 `e7` 只是“界”的 UTF-8 首字节；后缀以 `95 8c` 开始。因此不能分别对前缀、后缀调用字符串解码，也不能补替换字符。先拼接完整原始字节，再复用字符集、JSON 或二进制类型解码。测试显式证明前缀本身不是合法 UTF-8，但完整 SQL 文本精确一致。

## 五、返回值与兼容性

`ExternalField.Offset` 仍指向 20 字节引用，新增独立复制的 `Prefix`。`Length` 仍保存引用中的后缀长度，不改写为完整值长度。旧链返回 `Format="BLOB"`、`HeaderOffset=38`、`Version=0`；新链 Format 为空、HeaderOffset 为 0，Version 保持原契约。Reference 保留原始 20 字节，所以旧链第三字段依然可以从原始字节复核。

`Chunks` 只描述页外后缀，按逻辑顺序给出源页/偏移/长度。旧链没有新 LOB 索引项，IndexPage/IndexOffset 为 0。`Result.Pages` 继续仅列聚簇页。完整值，包括 COMPACT 前缀，受原有 16 MiB 上限约束。

实现入口：[schema.go](../../schema.go)、[metadata.go](../../metadata.go)、[record.go](../../record.go)、[compact.go](../../compact.go)、[lob.go](../../lob.go)。下一章解释旧页头和验收方法。
