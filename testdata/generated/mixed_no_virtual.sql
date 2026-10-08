mixed	CREATE TABLE `mixed` (
  `added` int NOT NULL DEFAULT '77',
  `id` bigint unsigned NOT NULL /*!80023 INVISIBLE */,
  `a` int DEFAULT NULL,
  `txt` longtext,
  `stored_n` int GENERATED ALWAYS AS ((`a` * 2)) STORED /*!80023 INVISIBLE */,
  `stored_text` longtext GENERATED ALWAYS AS (concat(`txt`,`txt`)) STORED,
  `tail` varchar(30) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
