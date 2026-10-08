hidden	CREATE TABLE `hidden` (
  `a` int DEFAULT NULL,
  `v` int GENERATED ALWAYS AS ((`a` + 1)) VIRTUAL,
  `stored_n` int GENERATED ALWAYS AS ((`a` * 2)) STORED,
  `secret` varchar(20) DEFAULT NULL /*!80023 INVISIBLE */
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
