CREATE TABLE `enum_labels` (
  `id` int NOT NULL,
  `value` enum('','ready','中文?','2','a''b','slash\\path','a,b') DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
