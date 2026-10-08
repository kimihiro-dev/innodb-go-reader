charset_latin1_swedish_ci	CREATE TABLE `charset_latin1_swedish_ci` (
  `id` int NOT NULL,
  `c` char(5) DEFAULT NULL,
  `v` varchar(256) DEFAULT NULL,
  `t` text,
  `cz` char(0) DEFAULT NULL,
  `bz` binary(0) DEFAULT NULL,
  `vz` varchar(0) DEFAULT NULL,
  `vbz` varbinary(0) DEFAULT NULL,
  `e` enum('','a','€') DEFAULT NULL,
  `s` set('a','€') DEFAULT NULL,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=latin1 ROW_FORMAT=DYNAMIC
