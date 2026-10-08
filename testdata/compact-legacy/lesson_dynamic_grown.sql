lesson_dynamic	CREATE TABLE `lesson_dynamic` (
  `id` int NOT NULL,
  `txt` longtext,
  `bin` longblob,
  `v` varchar(10000) DEFAULT NULL,
  `vb` varbinary(24000) DEFAULT NULL,
  `j` json DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
