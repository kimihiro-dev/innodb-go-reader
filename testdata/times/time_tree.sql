CREATE TABLE `time_tree` (
  `value` time(6) DEFAULT NULL,
  `id` bigint NOT NULL,
  `short_value` time(3) DEFAULT NULL,
  `note` varchar(100) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
