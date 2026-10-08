composite_tree	CREATE TABLE `composite_tree` (
  `payload` varchar(256) DEFAULT NULL,
  `c` int NOT NULL,
  `a` smallint NOT NULL,
  `n` int DEFAULT NULL,
  `b` smallint NOT NULL,
  PRIMARY KEY (`a`,`b`,`c`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
