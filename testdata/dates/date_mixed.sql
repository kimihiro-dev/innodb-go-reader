CREATE TABLE `date_mixed` (
  `d0` date DEFAULT NULL,
  `d1` year DEFAULT NULL,
  `d2` date DEFAULT NULL,
  `d3` year DEFAULT NULL,
  `d4` date DEFAULT NULL,
  `id` int NOT NULL,
  `d5` year DEFAULT NULL,
  `d6` date DEFAULT NULL,
  `d7` year DEFAULT NULL,
  `d8` date DEFAULT NULL,
  `d9` year DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
