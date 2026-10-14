CREATE TABLE `instant_tree_compact` (
  `id` int(11) NOT NULL,
  `v` varchar(2000) COLLATE utf8mb4_bin DEFAULT NULL,
  `a` int(11) DEFAULT '7',
  `b` int(11) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
