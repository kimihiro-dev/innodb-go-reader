charset_utf8mb4_general_ci	CREATE TABLE `charset_utf8mb4_general_ci` (
  `id` int NOT NULL,
  `c` char(5) COLLATE utf8mb4_general_ci DEFAULT NULL,
  `v` varchar(256) COLLATE utf8mb4_general_ci DEFAULT NULL,
  `t` text COLLATE utf8mb4_general_ci,
  `cz` char(0) COLLATE utf8mb4_general_ci DEFAULT NULL,
  `bz` binary(0) DEFAULT NULL,
  `vz` varchar(0) COLLATE utf8mb4_general_ci DEFAULT NULL,
  `vbz` varbinary(0) DEFAULT NULL,
  `e` enum('','a','?') COLLATE utf8mb4_general_ci DEFAULT NULL,
  `s` set('a','?') COLLATE utf8mb4_general_ci DEFAULT NULL,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci ROW_FORMAT=DYNAMIC
