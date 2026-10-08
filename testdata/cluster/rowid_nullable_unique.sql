rowid_nullable_unique	CREATE TABLE `rowid_nullable_unique` (
  `n` int DEFAULT NULL,
  `text` varchar(32) DEFAULT NULL,
  UNIQUE KEY `u` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
