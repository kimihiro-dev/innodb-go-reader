CREATE TABLE `double_values` (
  `id` int NOT NULL,
  `value` double DEFAULT NULL,
  `positive` double unsigned DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
