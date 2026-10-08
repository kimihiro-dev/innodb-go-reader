# Stage 34 secondary index snapshots

Twelve immutable MySQL 8.0.45 snapshots from the isolated temporary database
`innodb_reader_secondary_12402f8b6069` contain 5249 current clustered rows.
Twenty selected secondary trees contain 8150 live entries and 60 delete-marked
entries across 216 pages. The deep fixture has a level-2 root and 182 pages.

`manifest.json` records the environment and SHA256 of each decompressed ibd.
The instance uses 16KiB, crc32 and file-per-table. Tables were committed and
copied under `FLUSH TABLES ... FOR EXPORT`. For changes, a separate global
consistent read view retained old versions without holding table metadata locks.
Its transaction was then committed. The temporary instance was shut down
normally after capture; existing user tables and the original instance were
not modified.

- `*.ibd.gz`: original physical snapshots; never rewritten by tests.
- `*.sql`: SHOW CREATE TABLE output.
- `*.expected.json.gz`: independent full materialized SQL values (explicit columns).
- `*.secondary.json.gz`: FORCE INDEX SQL, ORDER BY and expected physical-field
  projection; binary fields use HEX, exact scalar strings remain strings.
- `*.sdi.json.gz`: official ibd2sdi output.
- `writer.sql.gz`, `holder.sql.gz`: actual SQL transcripts.
- `verification.json`: official CRC/SDI and CLI result checks.
- `physical.json`: derived Go structure counts and short real-byte samples.

Hidden ROW_ID is not SQL-accessible. Its physical locators are checked against
clustered ROW_ID; independent SQL verifies visible secondary fields, retaining
multiplicity. Deleted entries are separately retained physical evidence, not
SQL-current rows. Unique NULL values use the full physical key for ordering.

The overlap fixture deliberately has SDI elements `(code prefix, id)` but
physical fields `(code prefix, full code, id)`. The wide_prefix and compact
fixtures exercise original-column length encoding with 128..200-byte prefixes.

From the repository root:

```sh
go test -run '^TestSecondary' -v .
python3 scripts/verify_secondary_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
```

Recapture with `scripts/generate_secondary_fixtures.py --help` using a fresh
output directory and a dedicated instance. See the detailed
[layout manual](../../docs/format/71-secondary-record-layout.md) and
[validation manual](../../docs/format/72-secondary-validation.md).
