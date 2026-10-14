CREATE TABLE `empty_compact` (
  `id` int(11) NOT NULL,
  `payload` varchar(40) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
