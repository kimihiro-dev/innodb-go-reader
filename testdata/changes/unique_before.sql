unique	CREATE TABLE `unique` (
  `n` int NOT NULL,
  `k` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `v` varchar(256) DEFAULT NULL,
  UNIQUE KEY `u` (`k` DESC,`n` DESC),
  KEY `idx_n` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
