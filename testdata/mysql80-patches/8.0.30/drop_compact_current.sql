CREATE TABLE `drop_compact` (
  `a` int DEFAULT '7',
  `id` int NOT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
