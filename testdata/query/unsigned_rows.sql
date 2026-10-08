unsigned_rows	CREATE TABLE `unsigned_rows` (
  `id` bigint unsigned NOT NULL,
  `payload` int DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
