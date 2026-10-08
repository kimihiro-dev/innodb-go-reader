indexed	CREATE TABLE `indexed` (
  `id` int NOT NULL,
  `a` int DEFAULT NULL,
  `v` int GENERATED ALWAYS AS ((`a` + 1)) VIRTUAL,
  PRIMARY KEY (`id`),
  KEY `iv` (`v`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
