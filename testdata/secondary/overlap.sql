overlap	CREATE TABLE `overlap` (
  `code` varchar(100) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,
  `id` int NOT NULL,
  `payload` text COLLATE utf8mb4_bin,
  PRIMARY KEY (`code` DESC,`id`),
  KEY `prefix_idx` (`code`(2)),
  KEY `overlap_idx` (`id` DESC,`code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC
