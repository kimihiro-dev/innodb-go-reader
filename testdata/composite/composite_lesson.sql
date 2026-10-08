composite_lesson	CREATE TABLE `composite_lesson` (
  `text` varchar(32) DEFAULT NULL,
  `b` bigint unsigned NOT NULL,
  `n` int DEFAULT NULL,
  `a` smallint NOT NULL,
  `c` mediumint NOT NULL,
  `tail` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`a`,`b`,`c`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
