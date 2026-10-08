keys_utf8mb3_char_desc	CREATE TABLE `keys_utf8mb3_char_desc` (
  `before` varchar(12) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  `tie` int NOT NULL,
  `k` char(80) CHARACTER SET utf8mb3 COLLATE utf8mb3_bin NOT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`k` DESC,`tie`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
