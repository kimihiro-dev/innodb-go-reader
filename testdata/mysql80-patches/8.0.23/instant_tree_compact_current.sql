CREATE TABLE `instant_tree_compact` (
  `id` int NOT NULL,
  `v` varchar(2000) COLLATE utf8mb4_bin DEFAULT NULL,
  `a` int DEFAULT '7',
  `b` int DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
