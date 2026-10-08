prefixes	CREATE TABLE `prefixes` (
  `id` int NOT NULL,
  `name` varchar(64) COLLATE utf8mb4_bin DEFAULT NULL,
  `rank_value` int DEFAULT NULL,
  `marker` int NOT NULL,
  `payload` text COLLATE utf8mb4_bin,
  PRIMARY KEY (`id`),
  KEY `s` (`name`(3),`rank_value` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
