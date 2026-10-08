scalars	CREATE TABLE `scalars` (
  `id` int NOT NULL,
  `d` decimal(20,6) DEFAULT NULL,
  `dt` datetime(6) DEFAULT NULL,
  `tm` time(6) DEFAULT NULL,
  `ts` timestamp(6) NULL DEFAULT NULL,
  `b` bit(13) DEFAULT NULL,
  `y` year DEFAULT NULL,
  `da` date DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `d_idx` (`d` DESC),
  KEY `temporal_idx` (`dt`,`tm` DESC,`ts`),
  KEY `bits_idx` (`b`,`y`,`da`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
