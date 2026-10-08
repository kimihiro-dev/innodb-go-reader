CREATE TABLE `time_mixed` (
  `t0` time DEFAULT NULL,
  `t1` time(1) DEFAULT NULL,
  `t2` time(2) DEFAULT NULL,
  `t3` time(3) DEFAULT NULL,
  `id` int NOT NULL,
  `t4` time(4) DEFAULT NULL,
  `t5` time(5) DEFAULT NULL,
  `t6` time(6) DEFAULT NULL,
  `t7` time DEFAULT NULL,
  `t8` time(1) DEFAULT NULL,
  `stamp` datetime(6) DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
