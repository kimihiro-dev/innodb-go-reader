lesson	CREATE TABLE `lesson` (
  `id` int NOT NULL,
  `a` varchar(30) DEFAULT NULL,
  `b` int NOT NULL,
  `payload` longtext,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
