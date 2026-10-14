hashes_compact	CREATE TABLE `hashes_compact` (
  `id` int(11) NOT NULL,
  `n` int(11) NOT NULL,
  `v` varchar(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  `b` longblob,
  `d` decimal(24,8) DEFAULT NULL,
  `j` json DEFAULT NULL,
  PRIMARY KEY (`id`,`n`),
  KEY `s` (`n`),
  KEY `px` (`v`(3),`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT
/*!50100 PARTITION BY HASH (`n`)
PARTITIONS 3 */
