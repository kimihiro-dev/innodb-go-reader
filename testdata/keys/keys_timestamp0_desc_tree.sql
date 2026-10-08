keys_timestamp0_desc_tree	CREATE TABLE `keys_timestamp0_desc_tree` (
  `k` timestamp NOT NULL,
  `sequence` int NOT NULL,
  `payload` varchar(1500) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL,
  PRIMARY KEY (`k` DESC,`sequence` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
