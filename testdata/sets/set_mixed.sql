CREATE TABLE `set_mixed` (
  `s0` set('v0') DEFAULT NULL,
  `s1` set('v0','v1','v2','v3','v4','v5','v6','v7') DEFAULT NULL,
  `s2` set('v0','v1','v2','v3','v4','v5','v6','v7','v8') DEFAULT NULL,
  `s3` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15') DEFAULT NULL,
  `id` int NOT NULL,
  `s4` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15','v16') DEFAULT NULL,
  `s5` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15','v16','v17','v18','v19','v20','v21','v22','v23') DEFAULT NULL,
  `s6` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15','v16','v17','v18','v19','v20','v21','v22','v23','v24') DEFAULT NULL,
  `s7` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15','v16','v17','v18','v19','v20','v21','v22','v23','v24','v25','v26','v27','v28','v29','v30','v31','v32') DEFAULT NULL,
  `s8` set('v0','v1','v2','v3','v4','v5','v6','v7','v8','v9','v10','v11','v12','v13','v14','v15','v16','v17','v18','v19','v20','v21','v22','v23','v24','v25','v26','v27','v28','v29','v30','v31','v32','v33','v34','v35','v36','v37','v38','v39','v40','v41','v42','v43','v44','v45','v46','v47','v48','v49','v50','v51','v52','v53','v54','v55','v56','v57','v58','v59','v60','v61','v62','v63') DEFAULT NULL,
  `state` enum('','ready','中文?') DEFAULT NULL,
  `note` varchar(32) DEFAULT NULL,
  `body` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ROW_FORMAT=DYNAMIC;
