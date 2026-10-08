keys_varbinary_desc	CREATE TABLE `keys_varbinary_desc` (
  `k` varbinary(8) NOT NULL,
  `tie` int NOT NULL,
  PRIMARY KEY (`k` DESC,`tie`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
