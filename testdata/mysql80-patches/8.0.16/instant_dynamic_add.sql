CREATE TABLE `instant_dynamic` (
  `id` int(11) NOT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  `a` int(11) NOT NULL DEFAULT '7',
  `b` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
