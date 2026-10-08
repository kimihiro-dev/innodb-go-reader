keys_latin1_char_asc_tree	CREATE TABLE `keys_latin1_char_asc_tree` (
  `before` varchar(12) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `tie` int NOT NULL,
  `k` char(80) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,
  `n` int DEFAULT NULL,
  `sequence` int NOT NULL,
  `payload` varchar(1500) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  PRIMARY KEY (`k`,`tie` DESC,`sequence` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
