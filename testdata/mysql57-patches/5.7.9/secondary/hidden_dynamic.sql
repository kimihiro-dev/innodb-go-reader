CREATE TABLE `hidden_dynamic` (
  `seq` int(11) DEFAULT NULL,
  `name` varchar(30) COLLATE utf8mb4_bin DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin,
  KEY `s` (`seq`),
  KEY `px` (`name`(2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
