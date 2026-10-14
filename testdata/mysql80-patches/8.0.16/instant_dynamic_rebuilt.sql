CREATE TABLE `instant_dynamic` (
  `id` int(11) NOT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  `a` int(11) NOT NULL DEFAULT '99',
  `b` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  `c` varchar(20) COLLATE utf8mb4_bin NOT NULL DEFAULT '界?',
  `d` int(11) DEFAULT '42',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
