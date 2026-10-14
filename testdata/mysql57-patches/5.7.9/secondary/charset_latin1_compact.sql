CREATE TABLE `charset_latin1_compact` (
  `k` char(8) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,
  `id` int(11) NOT NULL,
  `v` varchar(30) CHARACTER SET latin1 COLLATE latin1_bin DEFAULT NULL,
  PRIMARY KEY (`k`,`id`),
  KEY `secondary` (`v`(2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
