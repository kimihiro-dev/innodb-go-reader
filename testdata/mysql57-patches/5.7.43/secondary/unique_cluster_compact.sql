CREATE TABLE `unique_cluster_compact` (
  `seq` int(11) DEFAULT NULL,
  `k` int(11) NOT NULL,
  `name` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  UNIQUE KEY `actual` (`k`),
  KEY `s` (`seq`),
  KEY `px` (`name`(2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
