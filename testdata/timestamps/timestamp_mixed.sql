CREATE TABLE `timestamp_mixed` (
  `t0` timestamp NULL DEFAULT NULL,
  `t1` timestamp(1) NULL DEFAULT NULL,
  `t2` timestamp(2) NULL DEFAULT NULL,
  `t3` timestamp(3) NULL DEFAULT NULL,
  `id` int NOT NULL,
  `t4` timestamp(4) NULL DEFAULT NULL,
  `t5` timestamp(5) NULL DEFAULT NULL,
  `t6` timestamp(6) NULL DEFAULT NULL,
  `t7` timestamp NULL DEFAULT NULL,
  `t8` timestamp(1) NULL DEFAULT NULL,
  `wall` datetime(6) DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
