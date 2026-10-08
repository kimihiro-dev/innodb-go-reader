keys_deep	CREATE TABLE `keys_deep` (
  `payload` varchar(1500) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  `tie` int NOT NULL,
  `k` varchar(192) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `binary` varbinary(768) NOT NULL,
  PRIMARY KEY (`k` DESC,`binary`,`tie` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
