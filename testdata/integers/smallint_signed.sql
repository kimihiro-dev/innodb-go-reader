CREATE TABLE `smallint_signed` (
  `tinyint_s` tinyint DEFAULT NULL,
  `tinyint_u` tinyint unsigned DEFAULT NULL,
  `smallint_s` smallint DEFAULT NULL,
  `smallint_u` smallint unsigned DEFAULT NULL,
  `mediumint_s` mediumint DEFAULT NULL,
  `id` smallint NOT NULL,
  `mediumint_u` mediumint unsigned DEFAULT NULL,
  `int_s` int DEFAULT NULL,
  `int_u` int unsigned DEFAULT NULL,
  `bigint_s` bigint DEFAULT NULL,
  `bigint_u` bigint unsigned DEFAULT NULL,
  `text` varchar(63) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
