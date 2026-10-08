CREATE TABLE `set_labels` (
  `id` int NOT NULL,
  `s0` set('','a','b') DEFAULT NULL,
  `s1` set('a','','b') DEFAULT NULL,
  `s2` set('a','b','') DEFAULT NULL,
  `s3` set('中文?','ready','2','a''b','slash\\path') DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
