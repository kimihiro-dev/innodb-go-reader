# Stage 31 — compact-legacy

MySQL 8.0.45, 16 KiB, isolated temporary instance; database `innodb_reader_compact_867fd3538ef5`.
Writer: `official-debug-noindex`. 22 immutable snapshots, 3866 SQL rows.

Official 8.0.45-debug writer, session `+d,lob_insert_noindex` forces old BLOB pages. This is not evidence of the release default or compatibility with historical MySQL versions. The mixed_formats phase disables that debug point for the txt update.

Each snapshot includes the exported ibd, explicit physical schema, SQL expected values, official ibd2sdi output, DDL and SQL index metadata. Binary SQL values use HEX; JSON SQL values use text. Hidden-key rows compare as multisets. Writer SQL and manifest preserve provenance. Derived physical/verification reports are not SQL oracles.

Reproduce with [generator](../../scripts/generate_compact_fixtures.py); offline verification uses [verifier](../../scripts/verify_compact_fixtures.py). See [chapters 65–66](../../docs/format/65-compact-row-layout.md). Temporary instances were shut down after capture; existing user tables were not changed.
