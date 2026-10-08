covering	CREATE TABLE `covering` (
  `id` bigint NOT NULL,
  `n` int DEFAULT NULL,
  `marker` int NOT NULL,
  `payload` varchar(100) COLLATE utf8mb4_bin DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `s` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT
