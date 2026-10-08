rowid_prefix_unique	CREATE TABLE `rowid_prefix_unique` (
  `text` varchar(32) NOT NULL,
  `n` int DEFAULT NULL,
  UNIQUE KEY `u` (`text`(2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
