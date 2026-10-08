CREATE TABLE `decimal_tree` (
  `amount` decimal(65,30) DEFAULT NULL,
  `id` bigint NOT NULL,
  `note` varchar(100) DEFAULT NULL,
  `whole` decimal(65,0) unsigned DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
