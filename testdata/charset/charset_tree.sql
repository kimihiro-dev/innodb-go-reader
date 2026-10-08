charset_tree	CREATE TABLE `charset_tree` (
  `id` int NOT NULL,
  `c` char(16) COLLATE latin1_bin DEFAULT NULL,
  `v` varchar(256) COLLATE latin1_bin DEFAULT NULL,
  `t` text COLLATE latin1_bin,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=latin1 COLLATE=latin1_bin ROW_FORMAT=DYNAMIC
