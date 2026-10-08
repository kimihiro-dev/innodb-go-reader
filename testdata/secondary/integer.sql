integer	CREATE TABLE `integer` (
  `id` bigint NOT NULL,
  `n` int DEFAULT NULL,
  `u` bigint unsigned DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `u_idx` (`u`),
  KEY `n_idx` (`n` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
