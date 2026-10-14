CREATE TABLE `lob_dynamic` (
  `id` int NOT NULL,
  `t` longtext COLLATE utf8mb4_bin,
  `b` longblob,
  `j` json DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
