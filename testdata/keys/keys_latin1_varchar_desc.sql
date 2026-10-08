keys_latin1_varchar_desc	CREATE TABLE `keys_latin1_varchar_desc` (
  `before` varchar(12) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `tie` int NOT NULL,
  `k` varchar(80) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`k` DESC,`tie`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
