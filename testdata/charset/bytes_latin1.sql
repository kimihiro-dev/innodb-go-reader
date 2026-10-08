bytes_latin1	CREATE TABLE `bytes_latin1` (
  `id` int NOT NULL,
  `c` char(1) DEFAULT NULL,
  `v` varchar(1) DEFAULT NULL,
  `t` tinytext,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=latin1 ROW_FORMAT=DYNAMIC
