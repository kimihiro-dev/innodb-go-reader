tree_compact	CREATE TABLE `tree_compact` (
  `added` int DEFAULT '88',
  `id` int NOT NULL,
  `padding` varchar(1600) NOT NULL,
  `v` int GENERATED ALWAYS AS ((`id` + 1)) VIRTUAL,
  `copy` varchar(1600) GENERATED ALWAYS AS (reverse(`padding`)) STORED,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=COMPACT
