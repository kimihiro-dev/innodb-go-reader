rowid	CREATE TABLE `rowid` (
  `n` int DEFAULT NULL,
  `k` varchar(30) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  KEY `n_idx` (`n`),
  KEY `k_idx` (`k` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
