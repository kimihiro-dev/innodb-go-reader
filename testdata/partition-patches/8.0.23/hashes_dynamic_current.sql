hashes_dynamic	CREATE TABLE `hashes_dynamic` (
  `id` int NOT NULL,
  `n` int NOT NULL,
  `v` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  `b` longblob,
  `d` decimal(24,8) DEFAULT NULL,
  `j` json DEFAULT NULL,
  PRIMARY KEY (`id`,`n`),
  KEY `s` (`n`),
  KEY `px` (`v`(3),`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
/*!50100 PARTITION BY HASH (`n`)
PARTITIONS 3 */
