keys_ascii_varchar_desc_tree	CREATE TABLE `keys_ascii_varchar_desc_tree` (
  `before` varchar(12) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `tie` int NOT NULL,
  `k` varchar(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `n` int DEFAULT NULL,
  `sequence` int NOT NULL,
  `payload` varchar(1500) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  PRIMARY KEY (`k` DESC,`tie`,`sequence` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
