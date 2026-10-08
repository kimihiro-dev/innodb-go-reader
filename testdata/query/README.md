# Stage 33 clustered-key queries

Captured from release MySQL 8.0.45 in isolated temporary instance
`/private/tmp/innodb-generated-stage30/instance`, database
`innodb_reader_query_801d543ea403`. The server used 16KiB pages, file-per-table
and crc32. Committed tables were copied under `FLUSH TABLES ... FOR EXPORT`.
The temporary server was shut down normally after read-only verification;
the original user instance, existing tables and global settings were untouched.

Three snapshots contain 2606 current rows. Saved SQL query results cover 92
queries and 3939 output rows, including signed/unsigned 64-bit endpoints,
missing keys, mixed ASC/DESC composite keys, latin1_bin PAD SPACE, prefixes,
reverse order and limits. Full provenance and raw SHA256 are in manifest.json.

- `*.ibd.gz`: immutable raw snapshots; manifest hashes refer to decompressed bytes.
- `*.json`: explicit schemas; `*.sql`: table DDL.
- `*.expected.json.gz`: independent full-table SQL values.
- `*.queries.json.gz`: query requests, SQL text and independent ordered SQL rows.
- `*.sdi.json.gz`: official ibd2sdi results; `*.indexes.json.gz`: SQL index metadata.
- `writer.sql.gz`: creation, insertion, capture and query SQL transcript.
- `coercion-probe.json.gz`: original mixed-row SQL probe using UTF-8 expressions
  that induced different collation semantics. Diagnostic evidence only.
- `physical.json` and `verification.json`: derived Go structure statistics and
  official CRC/SDI/CLI verification results, respectively.

The mixed-row query oracle was corrected by explicitly converting each UTF-8
literal to latin1 and applying latin1_bin. Forty queries were re-executed
read-only against the same unchanged tables. The raw snapshots were not
replaced. The generator includes the corrected predicates; the transcript
retains both the original and corrected queries.

Run offline verification from the repository root:

```sh
python3 scripts/verify_query_fixtures.py --mysql-bin /path/to/mysql-8.0.45/bin
go test -run '^TestQuery' -v .
```

For independent recapture, use `scripts/generate_query_fixtures.py --help`
and a fresh output directory. The script creates its own new database.
Details, real navigation bytes and boundaries are documented in
[chapter 69](../../docs/format/69-key-query-navigation.md) and
[chapter 70](../../docs/format/70-query-validation.md).
