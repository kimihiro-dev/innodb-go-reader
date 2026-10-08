CREATE TABLE `empty_variable` (
  `short_text` varchar(63) DEFAULT NULL,
  `long_text` varchar(1024) DEFAULT NULL,
  `id` int NOT NULL,
  `b255` varbinary(255) DEFAULT NULL,
  `b256` varbinary(256) DEFAULT NULL,
  `payload` varbinary(2048) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
