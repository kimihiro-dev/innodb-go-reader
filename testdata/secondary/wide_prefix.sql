wide_prefix	CREATE TABLE `wide_prefix` (
  `id` int NOT NULL,
  `v` varchar(400) CHARACTER SET latin1 COLLATE latin1_bin DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `v_idx` (`v`(200))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
