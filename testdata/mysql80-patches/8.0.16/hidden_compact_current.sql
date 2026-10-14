CREATE TABLE `hidden_compact` (
  `seq` int(11) DEFAULT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
