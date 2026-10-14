CREATE TABLE `secondary_compact` (
  `id` int NOT NULL,
  `n` int DEFAULT NULL,
  `v` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  PRIMARY KEY (`id`),
  KEY `s` (`n` DESC),
  KEY `px` (`v`(3),`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
