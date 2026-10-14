CREATE TABLE `ordinary_compact` (
  `id` int(11) NOT NULL,
  `n` int(11) DEFAULT NULL,
  `name` varchar(40) COLLATE utf8mb4_bin DEFAULT NULL,
  `vb` varbinary(40) DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  PRIMARY KEY (`id`),
  UNIQUE KEY `u` (`name`),
  KEY `s` (`n`),
  KEY `px` (`name`(3),`n`),
  KEY `bp` (`vb`(3),`n`),
  KEY `cover` (`n`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
