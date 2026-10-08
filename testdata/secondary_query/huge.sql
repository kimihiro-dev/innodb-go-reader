huge	CREATE TABLE `huge` (
  `id` int NOT NULL,
  `n` int NOT NULL,
  `marker` int NOT NULL,
  `payload` longblob,
  PRIMARY KEY (`id`),
  KEY `s` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
