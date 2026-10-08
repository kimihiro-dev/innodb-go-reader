composite_types	CREATE TABLE `composite_types` (
  `k0` tinyint NOT NULL,
  `k1` tinyint unsigned NOT NULL,
  `k2` smallint NOT NULL,
  `k3` smallint unsigned NOT NULL,
  `k4` mediumint NOT NULL,
  `k5` mediumint unsigned NOT NULL,
  `k6` int NOT NULL,
  `k7` int unsigned NOT NULL,
  `k8` bigint NOT NULL,
  `k9` bigint unsigned NOT NULL,
  PRIMARY KEY (`k9`,`k8`,`k7`,`k6`,`k5`,`k4`,`k3`,`k2`,`k1`,`k0`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC
