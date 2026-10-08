tree	CREATE TABLE `tree` (
  `id` bigint NOT NULL,
  `k` varbinary(255) NOT NULL,
  `v` varchar(1800) DEFAULT NULL,
  PRIMARY KEY (`k`,`id` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
