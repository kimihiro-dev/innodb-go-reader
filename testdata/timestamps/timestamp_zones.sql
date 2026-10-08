CREATE TABLE `timestamp_zones` (
  `id` int NOT NULL,
  `stamp` timestamp(6) NULL DEFAULT NULL,
  `wall` datetime(6) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
