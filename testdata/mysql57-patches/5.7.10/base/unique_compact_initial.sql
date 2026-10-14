CREATE TABLE `unique_compact` (
  `seq` int(11) DEFAULT NULL,
  `k` int(11) NOT NULL,
  `payload` varchar(20) COLLATE utf8mb4_bin DEFAULT NULL,
  UNIQUE KEY `actual_unique` (`k`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
