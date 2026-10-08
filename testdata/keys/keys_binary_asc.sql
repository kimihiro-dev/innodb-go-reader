keys_binary_asc	CREATE TABLE `keys_binary_asc` (
  `k` binary(8) NOT NULL,
  `tie` int NOT NULL,
  PRIMARY KEY (`k`,`tie`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
