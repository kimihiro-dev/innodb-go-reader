keys_ascii_char_asc	CREATE TABLE `keys_ascii_char_asc` (
  `before` varchar(12) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `tie` int NOT NULL,
  `k` char(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`k`,`tie` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
