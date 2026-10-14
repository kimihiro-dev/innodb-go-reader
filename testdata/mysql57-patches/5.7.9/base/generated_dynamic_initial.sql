CREATE TABLE `generated_dynamic` (
  `id` int(11) NOT NULL,
  `base` int(11) DEFAULT NULL,
  `v` int(11) GENERATED ALWAYS AS (base+100) VIRTUAL,
  `s` int(11) GENERATED ALWAYS AS (base+1) STORED,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
