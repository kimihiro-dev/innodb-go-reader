composite_dynamic	CREATE TABLE `composite_dynamic` (
  `seq` int NOT NULL,
  `k` varbinary(767) NOT NULL,
  `txt` varchar(1000) DEFAULT NULL,
  PRIMARY KEY (`k` DESC,`seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
