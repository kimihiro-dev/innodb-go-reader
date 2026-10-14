CREATE TABLE `charset_utf8mb4_dynamic` (
  `k` char(8) COLLATE utf8mb4_bin NOT NULL,
  `id` int(11) NOT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`k`,`id`),
  KEY `secondary` (`v`(2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
