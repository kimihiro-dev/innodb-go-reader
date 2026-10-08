CREATE TABLE `float_tree` (
  `f` float DEFAULT NULL,
  `id` bigint NOT NULL,
  `d` double DEFAULT NULL,
  `note` varchar(100) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
