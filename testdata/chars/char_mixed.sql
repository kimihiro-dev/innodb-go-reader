CREATE TABLE `char_mixed` (
  `c0` char(1) DEFAULT NULL,
  `c1` char(3) DEFAULT NULL,
  `c2` char(5) DEFAULT NULL,
  `c3` char(32) DEFAULT NULL,
  `id` int NOT NULL,
  `c4` char(63) DEFAULT NULL,
  `c5` char(64) DEFAULT NULL,
  `c6` char(127) DEFAULT NULL,
  `c7` char(192) DEFAULT NULL,
  `c8` char(255) DEFAULT NULL,
  `varying` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
