unique_single	CREATE TABLE `unique_single` (
  `text` varchar(32) DEFAULT NULL,
  `id` bigint unsigned NOT NULL,
  UNIQUE KEY `u` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
