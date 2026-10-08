tiny	CREATE TABLE `tiny` (
  `id` tinyint NOT NULL,
  `n` tinyint DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `n_idx` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
