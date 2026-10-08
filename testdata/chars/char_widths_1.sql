CREATE TABLE `char_widths_1` (
  `id` int NOT NULL,
  `c1` char(1) DEFAULT NULL,
  `c2` char(2) DEFAULT NULL,
  `c3` char(3) DEFAULT NULL,
  `c4` char(4) DEFAULT NULL,
  `c5` char(5) DEFAULT NULL,
  `c6` char(6) DEFAULT NULL,
  `c7` char(7) DEFAULT NULL,
  `c8` char(8) DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
