generated	CREATE TABLE `generated` (
  `id` int NOT NULL,
  `n` int DEFAULT NULL,
  `g` int GENERATED ALWAYS AS ((`n` * 2)) STORED /*!80023 INVISIBLE */,
  `v` int GENERATED ALWAYS AS ((`n` + 1)) VIRTUAL,
  `added` int DEFAULT '7',
  PRIMARY KEY (`id`),
  KEY `g_idx` (`g`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
