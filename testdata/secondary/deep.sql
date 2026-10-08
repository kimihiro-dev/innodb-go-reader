deep	CREATE TABLE `deep` (
  `a` varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  `id` int NOT NULL,
  `b` varchar(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `n` int DEFAULT NULL,
  PRIMARY KEY (`a` DESC,`id`),
  KEY `b_idx` (`b` DESC,`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
