CREATE TABLE `secondary_compact` (
  `id` int(11) NOT NULL,
  `n` int(11) DEFAULT NULL,
  `v` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  PRIMARY KEY (`id`),
  KEY `s` (`n` DESC),
  KEY `px` (`v`(3),`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
