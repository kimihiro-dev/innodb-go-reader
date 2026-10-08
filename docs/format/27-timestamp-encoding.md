# 27. TIMESTAMP：四字节秒数与 UTC

本章目标：从真实记录中找到 TIMESTAMP 字节，恢复秒数和小数，解释为什么它与 DATETIME 的时区行为不同。前置知识是 [记录与列值](04-record-to-values.md)、[DATETIME 小数秒](23-datetime-encoding.md)；负时长见 [TIME](25-time-encoding.md)。

## 类型语义先于字节解释

DATETIME 保存年月日时分秒分量；TIMESTAMP 保存时间点。SQL 插入 TIMESTAMP 时，会话时区参与转换；读取时，服务器再按读取会话的时区显示。物理字段里没有保存原插入会话的 `+08:00` 等设置，离线读取不能反推出它。

本实现返回 **UTC 字符串** `YYYY-MM-DD HH:MM:SS[.小数]`，小数位严格等于 schema 的 `fsp`。字符串不带 `Z`，但 TIMESTAMP 类型的输出契约始终是 UTC；消费端不能再把它当成本地时间。DATETIME 仍返回无时区分量字符串，TIME 仍返回时长。

| 情况 | 返回值（fsp=6） | 是否有字段数据字节 |
|---|---|---|
| SQL NULL | `nil` / JSON `null` | 无，由 NULL 位图标记 |
| MySQL 零时间戳 | `0000-00-00 00:00:00.000000` | 七字节全零 |
| 最小正常整秒 | `1970-01-01 00:00:01.000000` | 秒数为 1 |
| 正常时间点 | UTC 日期时间字符串 | 四字节秒数＋三字节小数 |

不要直接对零秒调用 `time.Unix(0,0)`：那会生成 1970 年纪元，丢失 MySQL 零值语义。当前支持范围中，零秒且非零小数明确报错，不将它悄悄转换成纪元或零日期。

## 当前 TIMESTAMP2 物理布局

SQL/schema 类型名仍为 `TIMESTAMP`；TIMESTAMP2 是这里采用的物理格式。定长字段不消费变长长度数组：

```text
字段起点 +0                         +4                  +4+ceil(fsp/2)
          ├──── UTC 整秒，大端 4 字节 ────┼──── 小数秒 0..3 字节 ────┤
```

| fsp | 秒数字节 | 小数字节 | 字段总长 | 小数原始整数 × 倍数 = 微秒 | `.123456` 截取对应精度的真实字节 |
|---|---:|---:|---:|---:|---|
| 0 | 4 | 0 | 4 | 无 | 无 |
| 1 | 4 | 1 | 5 | 10000 | `0a` → 100000 |
| 2 | 4 | 1 | 5 | 10000 | `0c` → 120000 |
| 3 | 4 | 2 | 6 | 100 | `04 ce` → 123000 |
| 4 | 4 | 2 | 6 | 100 | `04 d2` → 123400 |
| 5 | 4 | 3 | 7 | 1 | `01 e2 3a` → 123450 |
| 6 | 4 | 3 | 7 | 1 | `01 e2 40` → 123456 |

表中小数来自 `timestamp_fsp_N` 的 id=7。奇数 fsp 和相邻偶数 fsp 占相同字节数，但不能因此接受相同精度：微秒必须是 `10^(6-fsp)` 的整数倍。例如 fsp=1 的原始字节 `01` 代表 10000 微秒，不能用一位小数精确表示，必须拒绝。

秒数按大端读为 uint32，没有普通有符号 INT 那样的最高位翻转。当前契约允许正常秒数 `1..2147483647`；`80 00 00 00` 虽能作为 uint32 读取，却超过本阶段 MySQL 8.0.45 范围。小数必须在 `0..999999` 内并满足精度对齐。

## 真实记录逐步解码

资产：[timestamp_fsp_6.json](../../testdata/timestamps/timestamp_fsp_6.json)、[压缩文件](../../testdata/timestamps/timestamp_fsp_6.ibd.gz)，哈希见 [manifest](../../testdata/timestamps/manifest.json)。根页是 4，16 KiB 页的文件基址为 `4×16384=65536`。

id=7 的记录 origin 是页内 329，End 是 353。origin 之前六字节为 `00 04 00 48 00 1e`：首字节 NULL 位图为零，后五字节是记录头。数据布局如下：

| 页内区间 | 相对 origin | 长度 | 内容 |
|---|---:|---:|---|
| [329,333) | 0 | 4 | INT 主键 id |
| [333,339) | 4 | 6 | DB_TRX_ID |
| [339,346) | 10 | 7 | DB_ROLL_PTR |
| [346,350) | 17 | 4 | TIMESTAMP 秒数 `65 e0 79 f0` |
| [350,353) | 21 | 3 | 小数 `01 e2 40` |

因此字段文件偏移是 `65536+346=65882`。解码顺序：

1. `0x65e079f0 = 1709210096`，通过正常秒数范围检查。
2. `time.Unix(1709210096,0).UTC()` 得到 `2024-02-29 12:34:56`。
3. `0x01e240 = 123456` 微秒；fsp=6 的对齐单位是 1，合法。
4. 输出 `2024-02-29 12:34:56.123456`。始终先验证，再格式化；不使用浮点秒数。

同一页的边界字段：

| id | 字段页内起点 | 七字节原值 | 输出 |
|---:|---:|---|---|
| 1 | 166 | `00 00 00 00 00 00 00` | `0000-00-00 00:00:00.000000` |
| 2 | 196 | `00 00 00 01 00 00 00` | `1970-01-01 00:00:01.000000` |
| 3 | 226 | `7f ff ff ff 00 00 00` | `2038-01-19 03:14:07.000000` |
| 7 | 346 | `65 e0 79 f0 01 e2 40` | `2024-02-29 12:34:56.123456` |
| 11 | 466 | `7f ff ff ff 0f 42 3f` | `2038-01-19 03:14:07.999999` |

id=0 是 NULL：origin=126、End=143，只占主键和两个系统字段共 17 字节，不能继续读取七字节“时间戳”。普通非 NULL 记录的数据部分占 `17+timestampWidth(fsp)` 字节，记录头和位图另计。

## 代码与源码依据

- [schema.go](../../schema.go)：TIMESTAMP 的 fsp=0..6，拒绝 unsigned、长度和 DECIMAL 属性；主键仍限整数。
- [timestamp.go](../../timestamp.go)：`timestampWidth` 定宽，`decodeTimestamp` 还原、验证并生成 UTC 字符串。
- [record.go](../../record.go)：NULL 判断之后读取定长字节；错误包含列名，整表读取失败时没有部分结果。

本阶段核对了 MySQL 官方 mysql-8.0.45 标签的 [my_time.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/mysys/my_time.cc) 中 `my_timestamp_to_binary` / `my_timestamp_from_binary`（2017–2070 行），以及 [field.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/sql/field.cc) 的转换范围检查和 `Field_timestampf::get_date_internal_at`（5229–5235 行）。后者对零秒单独处理，对正常秒数通过时区对象转换。InnoDB 在 [ha_innodb.cc](https://github.com/mysql/mysql-server/blob/mysql-8.0.45/storage/innobase/handler/ha_innodb.cc) 将 TIMESTAMP2 作为 DATA_FIXBINARY 存储。

以上只覆盖当前新建、只插入表中的格式。不恢复插入前舍入精度、源会话时区、自动更新时间列行为、旧格式或事务历史；显式 schema 仍须可信。完整时区实验与复现见 [下一章](28-timestamp-validation.md)。
