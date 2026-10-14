CREATE TABLE `lob_dynamic` (
  `id` int(11) NOT NULL,
  `txt` longtext COLLATE utf8mb4_bin,
  `bin` longblob,
  `v` varchar(8000) COLLATE utf8mb4_bin DEFAULT NULL,
  `vb` varbinary(20000) DEFAULT NULL,
  `j` json DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
