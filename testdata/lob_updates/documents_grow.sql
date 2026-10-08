documents	CREATE TABLE `documents` (
  `id` int NOT NULL,
  `j` json DEFAULT NULL,
  `n` int NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
