CREATE TABLE `decimal_lesson` (
  `amount` decimal(14,4) DEFAULT NULL,
  `note` varchar(40) DEFAULT NULL,
  `id` int NOT NULL,
  `tiny` decimal(2,2) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
