CREATE TABLE `tree_dynamic` (
  `id` int(11) NOT NULL,
  `k` varbinary(767) NOT NULL,
  `payload` varchar(1200) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `secondary` (`k`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
