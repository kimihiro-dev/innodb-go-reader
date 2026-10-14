CREATE TABLE `tree_compact` (
  `k` varbinary(767) NOT NULL,
  `seq` int(11) NOT NULL,
  `padding` varchar(1400) COLLATE utf8mb4_bin NOT NULL,
  PRIMARY KEY (`k`,`seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
