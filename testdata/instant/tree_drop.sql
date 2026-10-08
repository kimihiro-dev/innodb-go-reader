tree	CREATE TABLE `tree` (
  `m8` varchar(10) DEFAULT 'x',
  `m7` varchar(10) DEFAULT 'x',
  `m6` varchar(10) DEFAULT 'x',
  `m5` varchar(10) DEFAULT 'x',
  `m4` varchar(10) DEFAULT 'x',
  `m3` varchar(10) DEFAULT 'x',
  `m2` varchar(10) DEFAULT 'x',
  `m1` varchar(10) DEFAULT 'x',
  `m0` varchar(10) DEFAULT 'x',
  `id` int NOT NULL,
  `padding` varchar(1600) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
