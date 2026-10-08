external_ascii	CREATE TABLE `external_ascii` (
  `id` int NOT NULL,
  `tiny` tinytext,
  `doc` mediumtext,
  `longdoc` longtext,
  `note` varchar(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=ascii ROW_FORMAT=DYNAMIC
