CREATE TABLE `instant_virtual_dynamic` (
  `id` int(11) NOT NULL,
  `v` int(11) GENERATED ALWAYS AS ((`id` + 1)) VIRTUAL,
  `n` int(11) DEFAULT NULL,
  `a` int(11) DEFAULT '7',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
