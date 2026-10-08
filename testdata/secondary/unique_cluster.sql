unique_cluster	CREATE TABLE `unique_cluster` (
  `a` int NOT NULL,
  `n` int DEFAULT NULL,
  `u` int DEFAULT NULL,
  UNIQUE KEY `cluster_key` (`a`),
  UNIQUE KEY `u_idx` (`u`),
  KEY `n_idx` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
