# Stage 31 — compact

MySQL 8.0.45, 16 KiB, isolated temporary instance; database `innodb_reader_compact_01d906c9c5fa`.
Writer: `normal`. 22 immutable snapshots, 3866 SQL rows.

Ordinary release writer. COMPACT external fields have a 768-byte prefix followed by a new LOB reference.

Each snapshot includes the exported ibd, explicit physical schema, SQL expected values, official ibd2sdi output, DDL and SQL index metadata. Binary SQL values use HEX; JSON SQL values use text. Hidden-key rows compare as multisets. Writer SQL and manifest preserve provenance. Derived physical/verification reports are not SQL oracles.

Reproduce with [generator](../../scripts/generate_compact_fixtures.py); offline verification uses [verifier](../../scripts/verify_compact_fixtures.py). See [chapters 65–66](../../docs/format/65-compact-row-layout.md). Temporary instances were shut down after capture; existing user tables were not changed.
