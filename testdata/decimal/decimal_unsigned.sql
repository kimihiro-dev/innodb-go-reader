CREATE TABLE `decimal_unsigned` (
  `id` int NOT NULL,
  `amount` decimal(65,30) unsigned DEFAULT NULL,
  `fraction` decimal(30,30) unsigned DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
