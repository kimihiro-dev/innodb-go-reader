CREATE TABLE `generated_compact` (
  `id` int(11) NOT NULL,
  `n` int(11) DEFAULT NULL,
  `s` bigint(20) GENERATED ALWAYS AS ((`n` * 3)) STORED,
  `v` bigint(20) GENERATED ALWAYS AS ((`n` + 1)) VIRTUAL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
