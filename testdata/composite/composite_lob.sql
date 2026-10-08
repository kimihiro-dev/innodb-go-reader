composite_lob	CREATE TABLE `composite_lob` (
  `a` int NOT NULL,
  `doc` mediumtext,
  `c` bigint unsigned NOT NULL,
  `b` tinyint NOT NULL,
  PRIMARY KEY (`a`,`b`,`c`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
