CREATE TABLE `float_mixed` (
  `f0` float DEFAULT NULL,
  `f1` double DEFAULT NULL,
  `f2` float DEFAULT NULL,
  `f3` double DEFAULT NULL,
  `f4` float DEFAULT NULL,
  `id` int NOT NULL,
  `f5` double DEFAULT NULL,
  `f6` float DEFAULT NULL,
  `f7` double DEFAULT NULL,
  `f8` float DEFAULT NULL,
  `f9` double DEFAULT NULL,
  `exact` decimal(14,4) DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
