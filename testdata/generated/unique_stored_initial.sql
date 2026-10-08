unique_stored	CREATE TABLE `unique_stored` (
  `a` int NOT NULL,
  `v` int GENERATED ALWAYS AS ((`a` - 1)) VIRTUAL,
  `stored_n` int GENERATED ALWAYS AS ((`a` + 1)) STORED NOT NULL,
  UNIQUE KEY `actual` (`stored_n`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
