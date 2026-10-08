stored_values	CREATE TABLE `stored_values` (
  `id` int NOT NULL,
  `a` int DEFAULT NULL,
  `text` varchar(40) DEFAULT NULL,
  `twice` int GENERATED ALWAYS AS ((`a` * 2)) STORED,
  `label` varchar(90) GENERATED ALWAYS AS (concat(`text`,_utf8mb4':',`a`)) STORED,
  `secret` varbinary(12) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
