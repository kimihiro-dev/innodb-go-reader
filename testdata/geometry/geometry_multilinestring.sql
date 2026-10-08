geometry_multilinestring	CREATE TABLE `geometry_multilinestring` (
  `id` int NOT NULL,
  `doc` multilinestring DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
