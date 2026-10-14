hidden_dynamic	CREATE TABLE `hidden_dynamic` (
  `id` int(11) DEFAULT NULL,
  `n` int(11) DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  KEY `s` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
/*!50100 PARTITION BY HASH (n)
PARTITIONS 3 */
