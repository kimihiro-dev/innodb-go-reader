CREATE TABLE `empty_compact` (
  `id` int(11) NOT NULL,
  `n` int(11) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `secondary` (`n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
