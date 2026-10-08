CREATE TABLE `lob_mixed_tree` (
  `text` varchar(6000) DEFAULT NULL,
  `id` bigint unsigned NOT NULL,
  `data` varbinary(30000) DEFAULT NULL,
  `tag` varchar(128) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
