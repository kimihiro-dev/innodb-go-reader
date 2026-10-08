charset_utf8_alias	CREATE TABLE `charset_utf8_alias` (
  `id` int NOT NULL,
  `c` char(5) DEFAULT NULL,
  `v` varchar(32) DEFAULT NULL,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb3 ROW_FORMAT=DYNAMIC
