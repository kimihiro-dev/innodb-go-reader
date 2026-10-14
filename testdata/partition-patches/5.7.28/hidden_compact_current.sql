hidden_compact	CREATE TABLE `hidden_compact` (
  `id` int(11) DEFAULT NULL,
  `n` int(11) DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  KEY `s` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT
/*!50100 PARTITION BY HASH (n)
PARTITIONS 3 */
