generated_dynamic	CREATE TABLE `generated_dynamic` (
  `id` int NOT NULL,
  `n` int NOT NULL,
  `s` bigint GENERATED ALWAYS AS ((`n` * 3)) STORED,
  `v` bigint GENERATED ALWAYS AS ((`n` + 1)) VIRTUAL,
  PRIMARY KEY (`id`,`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
/*!50100 PARTITION BY HASH (`n`)
PARTITIONS 3 */
