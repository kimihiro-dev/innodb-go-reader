primary_secondary	CREATE TABLE `primary_secondary` (
  `id` int NOT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `n_idx` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
