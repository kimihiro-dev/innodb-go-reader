charset_latin1_bin	CREATE TABLE `charset_latin1_bin` (
  `id` int NOT NULL,
  `c` char(5) COLLATE latin1_bin DEFAULT NULL,
  `v` varchar(256) COLLATE latin1_bin DEFAULT NULL,
  `t` text COLLATE latin1_bin,
  `cz` char(0) COLLATE latin1_bin DEFAULT NULL,
  `bz` binary(0) DEFAULT NULL,
  `vz` varchar(0) COLLATE latin1_bin DEFAULT NULL,
  `vbz` varbinary(0) DEFAULT NULL,
  `e` enum('','a','€') COLLATE latin1_bin DEFAULT NULL,
  `s` set('a','€') COLLATE latin1_bin DEFAULT NULL,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=latin1 COLLATE=latin1_bin ROW_FORMAT=DYNAMIC
