compact	CREATE TABLE `compact` (
  `id` int NOT NULL,
  `c` char(40) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin DEFAULT NULL,
  `l` char(40) CHARACTER SET latin1 COLLATE latin1_bin DEFAULT NULL,
  `b` varbinary(300) DEFAULT NULL,
  `f` binary(8) NOT NULL,
  PRIMARY KEY (`id`),
  KEY `c_idx` (`c`(4) DESC,`l`(4)),
  KEY `b_idx` (`b`(160),`f`(4))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT
