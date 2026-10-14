CREATE TABLE `gipk` (
  `my_row_id` bigint unsigned NOT NULL AUTO_INCREMENT /*!80023 INVISIBLE */,
  `n` int DEFAULT NULL,
  `v` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`my_row_id`),
  KEY `s` (`n`)
) ENGINE=InnoDB AUTO_INCREMENT=4 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
