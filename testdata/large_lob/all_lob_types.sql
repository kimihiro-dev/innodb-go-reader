CREATE TABLE `all_lob_types` (
  `id` int NOT NULL,
  `tinytext` tinytext,
  `tinyblob` tinyblob,
  `text` text,
  `blob` blob,
  `mediumtext` mediumtext,
  `mediumblob` mediumblob,
  `longtext` longtext,
  `longblob` longblob,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
