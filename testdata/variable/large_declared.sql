CREATE TABLE `large_declared` (
  `id` int NOT NULL,
  `text` varchar(12000) DEFAULT NULL,
  `data` varbinary(16000) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
