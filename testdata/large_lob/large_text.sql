CREATE TABLE `large_text` (
  `id` int NOT NULL,
  `medium` mediumtext,
  `long` longtext,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
