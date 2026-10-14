CREATE TABLE `instant_virtual_compact` (
  `id` int NOT NULL,
  `v` int GENERATED ALWAYS AS ((`id` + 1)) VIRTUAL,
  `n` int DEFAULT NULL,
  `a` int DEFAULT '7',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=COMPACT;
