CREATE TABLE `generated_compact` (
  `id` int NOT NULL,
  `n` int DEFAULT NULL,
  `s` bigint GENERATED ALWAYS AS ((`n` * 3)) STORED,
  `v` bigint GENERATED ALWAYS AS ((`n` + 1)) VIRTUAL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
