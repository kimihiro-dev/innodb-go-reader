#!/usr/bin/env python3
"""Capture index add/drop/recreate snapshots in a new database; use MYSQL_PWD.

Leaves the new database for inspection. Never restarts MySQL or changes globals.
Requires local access to datadir and mysql/ibd2sdi/innochecksum executables.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mysql', required=True)
    parser.add_argument('--socket', required=True)
    parser.add_argument('--user', default='root')
    parser.add_argument('--ibd2sdi', required=True)
    parser.add_argument('--innochecksum', required=True)
    parser.add_argument('--out', required=True, help='New directory, must not exist')
    args = parser.parse_args()
    out = Path(args.out).resolve()
    if out.exists():
        parser.error('output directory already exists')
    out.mkdir(parents=True)
    database = 'innodb_reader_metadata_' + uuid.uuid4().hex[:12]
    manifest = dict(database=database, generated_at=datetime.now(timezone.utc).isoformat(),
                    snapshot='Same connection: FOR EXPORT, SQL evidence, copy, UNLOCK',
                    status='incomplete', cases=[])

    def save(name, value):
        (out / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')

    proc = subprocess.Popen([args.mysql, '--no-defaults', '--user=' + args.user,
                             '--socket=' + args.socket, '--default-character-set=utf8mb4',
                             '--batch', '--raw', '--skip-column-names', '--unbuffered'],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            text=True, encoding='utf-8', env=os.environ.copy())
    sql_log = out / 'generate.sql'

    def query(sql):
        with sql_log.open('a', encoding='utf-8') as log:
            log.write(sql.rstrip(';') + ';\n')
        marker = '__metadata_end_' + uuid.uuid4().hex
        proc.stdin.write(sql.rstrip(';') + ";\nSELECT '" + marker + "';\n")
        proc.stdin.flush()
        result = []
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError('mysql exited before completing SQL; see generate.sql')
            line = line.rstrip('\n')
            if line == marker:
                return result
            result.append(line)

    try:
        environment = json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,"
                                       "'page_size',@@innodb_page_size,'file_per_table',@@innodb_file_per_table,"
                                       "'checksum',@@innodb_checksum_algorithm,'sql_mode',@@SESSION.sql_mode)")[0])
        manifest['environment'] = environment
        if (environment['version'] != '8.0.45' or environment['page_size'] != 16384
                or not environment['file_per_table'] or environment['checksum'] not in ('crc32', 'strict_crc32')):
            raise RuntimeError('requires MySQL 8.0.45, 16 KiB, file_per_table and crc32; no settings changed')
        query(f'CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci')
        query(f'USE `{database}`')
        query('CREATE TABLE sample (id INT NOT NULL PRIMARY KEY, score INT NULL, name VARCHAR(32) NULL) '
              "ENGINE=InnoDB ROW_FORMAT=DYNAMIC ENCRYPTION='N'")
        query("INSERT INTO sample VALUES (7,0,''),(-3,42,'InnoDB'),(12,-5,'你好'),(2,NULL,NULL)")
        stages = [('baseline', None), ('added', 'CREATE INDEX idx_score ON sample(score)'),
                  ('dropped', 'DROP INDEX idx_score ON sample'),
                  ('recreated', 'CREATE INDEX idx_score ON sample(score)'),
                  ('final', 'DROP INDEX idx_score ON sample')]
        for name, ddl in stages:
            if ddl:
                query(ddl)
            query('FLUSH TABLES sample FOR EXPORT')
            try:
                rows = [json.loads(x) for x in query('SELECT JSON_ARRAY(id,score,name) FROM sample ORDER BY id')]
                indexes = [json.loads(x) for x in query(
                    "SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,"
                    "'space_id',i.SPACE,'table_id',i.TABLE_ID) FROM INFORMATION_SCHEMA.INNODB_INDEXES i "
                    "JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "
                    f"WHERE t.NAME='{database}/sample' ORDER BY i.NAME")]
                (out / (name + '.sql')).write_text('\n'.join(query('SHOW CREATE TABLE sample')) + '\n', encoding='utf-8')
                dest = out / (name + '.ibd')
                shutil.copyfile(Path(environment['datadir']) / database / 'sample.ibd', dest)
            finally:
                query('UNLOCK TABLES')
            save(name + '.expected.json', rows)
            save(name + '.indexes.json', indexes)
            with (out / (name + '.sdi.json')).open('w', encoding='utf-8') as output:
                subprocess.run([args.ibd2sdi, str(dest)], stdout=output, check=True)
            subprocess.run([args.innochecksum, '--strict-check=crc32', str(dest)], check=True)
            manifest['cases'].append(dict(name=name, rows=len(rows), indexes=indexes,
                                          sha256=hashlib.sha256(dest.read_bytes()).hexdigest(),
                                          readable=name in ('baseline', 'dropped', 'final')))
            save('manifest.json', manifest)
        manifest['status'] = 'captured; parser verification required'
    finally:
        save('manifest.json', manifest)
        proc.stdin.close()  # Connection close releases locks even after a failed query.
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.terminate()  # Only our mysql client, never mysqld.
            proc.wait(timeout=15)
        proc.stdout.close()
    print(json.dumps(dict(output=str(out), database=database), ensure_ascii=False))


if __name__ == '__main__':
    main()
