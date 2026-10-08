mixed_rows	CREATE TABLE `mixed_rows` (
  `tenant` int NOT NULL,
  `code` varchar(16) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,
  `seq` bigint unsigned NOT NULL,
  `payload` varchar(1600) DEFAULT NULL,
  PRIMARY KEY (`tenant`,`code` DESC,`seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=COMPACT
