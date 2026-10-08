CREATE TABLE `binary_mixed` (
  `b0` binary(1) DEFAULT NULL,
  `b1` binary(2) DEFAULT NULL,
  `b2` binary(3) DEFAULT NULL,
  `b3` binary(7) DEFAULT NULL,
  `id` int NOT NULL,
  `b4` binary(8) DEFAULT NULL,
  `b5` binary(16) DEFAULT NULL,
  `b6` binary(63) DEFAULT NULL,
  `b7` binary(128) DEFAULT NULL,
  `b8` binary(255) DEFAULT NULL,
  `varying` varbinary(255) DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
