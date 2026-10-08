geometry_srid	CREATE TABLE `geometry_srid` (
  `id` int NOT NULL,
  `doc` point /*!80003 SRID 4326 */ DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
