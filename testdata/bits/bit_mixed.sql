CREATE TABLE `bit_mixed` (
  `b0` bit(1) DEFAULT NULL,
  `b1` bit(7) DEFAULT NULL,
  `b2` bit(8) DEFAULT NULL,
  `b3` bit(9) DEFAULT NULL,
  `id` int NOT NULL,
  `b4` bit(15) DEFAULT NULL,
  `b5` bit(16) DEFAULT NULL,
  `b6` bit(31) DEFAULT NULL,
  `b7` bit(63) DEFAULT NULL,
  `b8` bit(64) DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
