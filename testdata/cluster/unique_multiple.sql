unique_multiple	CREATE TABLE `unique_multiple` (
  `nullable` int DEFAULT NULL,
  `b` int NOT NULL,
  `a` int NOT NULL,
  UNIQUE KEY `z_chosen` (`b`),
  UNIQUE KEY `a_later` (`a`),
  UNIQUE KEY `nullable_first` (`nullable`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
