keys_lesson	CREATE TABLE `keys_lesson` (
  `doc` mediumtext,
  `tie` int NOT NULL,
  `k` varbinary(768) NOT NULL,
  `text` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`text` DESC,`k`,`tie` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
