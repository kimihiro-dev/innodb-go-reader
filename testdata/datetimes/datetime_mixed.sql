CREATE TABLE `datetime_mixed` (
  `d0` datetime DEFAULT NULL,
  `d1` datetime(1) DEFAULT NULL,
  `d2` datetime(2) DEFAULT NULL,
  `d3` datetime(3) DEFAULT NULL,
  `id` int NOT NULL,
  `d4` datetime(4) DEFAULT NULL,
  `d5` datetime(5) DEFAULT NULL,
  `d6` datetime(6) DEFAULT NULL,
  `d7` datetime DEFAULT NULL,
  `d8` datetime(1) DEFAULT NULL,
  `day` date DEFAULT NULL,
  `year` year DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
