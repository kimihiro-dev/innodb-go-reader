lesson	CREATE TABLE `lesson` (
  `c` int NOT NULL DEFAULT '77',
  `id` int NOT NULL,
  `d` varchar(30) DEFAULT NULL,
  `a` varchar(30) DEFAULT NULL,
  `b` int NOT NULL,
  `payload` longtext,
  `e` varbinary(20) NOT NULL DEFAULT 0x00FF,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
