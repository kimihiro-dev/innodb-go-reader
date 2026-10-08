lesson	CREATE TABLE `lesson` (
  `id` int NOT NULL,
  `d` varchar(30) DEFAULT NULL,
  `b` int NOT NULL,
  `e` varbinary(20) NOT NULL DEFAULT 0x00FF,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
