CREATE TABLE `float_values` (
  `id` int NOT NULL,
  `value` float DEFAULT NULL,
  `positive` float unsigned DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
