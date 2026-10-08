CREATE TABLE `variable_rows` (
  `id` int NOT NULL,
  `left_text` varchar(63) DEFAULT NULL,
  `right_text` varchar(32) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
