unique_text_tree	CREATE TABLE `unique_text_tree` (
  `payload` varchar(1200) DEFAULT NULL,
  `n` int NOT NULL,
  `k` varchar(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  UNIQUE KEY `u` (`k` DESC,`n` DESC),
  KEY `secondary_n` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
