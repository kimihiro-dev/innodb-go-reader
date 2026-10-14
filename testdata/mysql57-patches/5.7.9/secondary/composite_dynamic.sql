CREATE TABLE `composite_dynamic` (
  `k` varbinary(24) NOT NULL,
  `id` int(11) NOT NULL,
  `n` int(11) DEFAULT NULL,
  `name` varchar(40) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`k`,`id`),
  KEY `px` (`k`(3),`name`(2)),
  KEY `dedup` (`n`,`k`,`id`),
  KEY `s` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
