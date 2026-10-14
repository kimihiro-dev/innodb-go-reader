CREATE TABLE `families_dynamic` (
  `id` int(11) NOT NULL,
  `tt` tinytext COLLATE utf8mb4_bin,
  `t` text COLLATE utf8mb4_bin,
  `mt` mediumtext COLLATE utf8mb4_bin,
  `lt` longtext COLLATE utf8mb4_bin,
  `tb` tinyblob,
  `b` blob,
  `mb` mediumblob,
  `lb` longblob,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
