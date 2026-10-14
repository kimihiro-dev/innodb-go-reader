CREATE TABLE `instant_defaults_dynamic` (
  `id` int(11) NOT NULL,
  `d` decimal(10,3) NOT NULL DEFAULT '-12.345',
  `v` varbinary(20) NOT NULL DEFAULT '\0?',
  `e` enum('','界','abc') COLLATE utf8mb4_bin NOT NULL DEFAULT '界',
  `t` datetime(6) NOT NULL DEFAULT '2024-02-29 12:34:56.123456',
  `z` varchar(20) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
