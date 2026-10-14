CREATE TABLE `hidden_dynamic` (
  `seq` int(11) DEFAULT NULL,
  `payload` longtext COLLATE utf8mb4_bin
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
