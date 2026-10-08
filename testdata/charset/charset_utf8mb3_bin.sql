charset_utf8mb3_bin	CREATE TABLE `charset_utf8mb3_bin` (
  `id` int NOT NULL,
  `c` char(5) COLLATE utf8mb3_bin DEFAULT NULL,
  `v` varchar(256) COLLATE utf8mb3_bin DEFAULT NULL,
  `t` text COLLATE utf8mb3_bin,
  `cz` char(0) COLLATE utf8mb3_bin DEFAULT NULL,
  `bz` binary(0) DEFAULT NULL,
  `vz` varchar(0) COLLATE utf8mb3_bin DEFAULT NULL,
  `vbz` varbinary(0) DEFAULT NULL,
  `e` enum('','a','中') COLLATE utf8mb3_bin DEFAULT NULL,
  `s` set('a','中') COLLATE utf8mb3_bin DEFAULT NULL,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb3 COLLATE=utf8mb3_bin ROW_FORMAT=DYNAMIC
