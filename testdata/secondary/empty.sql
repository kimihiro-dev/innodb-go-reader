empty	CREATE TABLE `empty` (
  `id` int NOT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `n_idx` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
