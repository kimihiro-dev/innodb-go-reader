CREATE TABLE `timestamp_tree` (
  `value` timestamp(6) NULL DEFAULT NULL,
  `id` bigint NOT NULL,
  `short_value` timestamp(3) NULL DEFAULT NULL,
  `note` varchar(100) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
