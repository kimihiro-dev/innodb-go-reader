#!/usr/bin/env python3
"""Capture initial JSON values in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import uuid
from datetime import datetime, timezone


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mysql', required=True)
    parser.add_argument('--socket', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out).resolve()
    if out.exists():
        parser.error('output must not exist')
    out.mkdir(parents=True)
    database = 'innodb_reader_json_' + uuid.uuid4().hex[:12]
    proc = subprocess.Popen([args.mysql, '--no-defaults', '-uroot', '--socket='+args.socket,
                             '--default-character-set=utf8mb4', '--max-allowed-packet=128M',
                             '--batch', '--raw', '--skip-column-names', '--unbuffered'],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, encoding='utf-8', env=os.environ.copy())
    log = []

    def query(sql):
        log.append(sql+';')
        marker = '__end_'+uuid.uuid4().hex
        proc.stdin.write(sql+";\nSELECT '"+marker+"';\n")
        proc.stdin.flush()
        lines = []
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError('mysql exited; see generate.sql.gz')
            line = line.rstrip('\n')
            if line == marker:
                return lines
            lines.append(line)

    def save(name, value):
        data = (json.dumps(value, ensure_ascii=False, indent=2)+'\n').encode()
        (out/name).write_bytes(gzip.compress(data, mtime=0))

    def cast_json(value):
        # Hex UTF-8 avoids dependence on SQL backslash escaping.
        text = json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode().hex()
        return "CAST(CONVERT(X'"+text+"' USING utf8mb4) AS JSON)"

    lesson = ['NULL'] + [cast_json(v) for v in [None, True, False, {}, [], '', '中文😀\x00\n',
        -32769,-32768,32767,32768,-2147483649,-2147483648,2147483647,2147483648,
        -9223372036854775808,9223372036854775807,18446744073709551615,1.25,-0.0,
        {'b':[True,None,{'中文':'值'}],'aa':-9},[1,32768,2147483648,'x']]]
    opaque = ["JSON_EXTRACT(JSON_ARRAY(CAST('12345678901234567890.1234567890' AS DECIMAL(40,10))),'$[0]')",
        "JSON_ARRAY(CAST('-0.0001' AS DECIMAL(8,4)),CAST('2024-02-29' AS DATE),CAST('-25:02:03.123456' AS TIME(6)),CAST('2024-02-29 12:34:56.123456' AS DATETIME(6)))",
        "JSON_ARRAY(CAST(X'00FF4142' AS BINARY(4)),CAST('' AS BINARY(0)),CAST(REPEAT('x',100) AS BINARY(100)))"]
    nested = 1
    for _ in range(98): nested = [nested]
    cases = [('json_lesson', lesson), ('json_opaque', opaque),
        ('json_boundaries', [cast_json('x'*n) for n in [127,128,255,256,16383,16384]] + [cast_json(nested)]),
        ('json_large', [cast_json(['x'*33000, 32768, 2147483648, None, True, 'y'*33000]),cast_json({'b':'b'*33000,'aa':'a'*33000}),cast_json([0]*22000)]),
        ('json_tree', [cast_json({'id':i,'text':'中文😀'*12,'values':[i,None,i*1.25]}) for i in range(600)])]
    manifest = dict(database=database, generated_at=datetime.now(timezone.utc).isoformat(), cases=[], status='incomplete')
    try:
        env = json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0])
        manifest['environment'] = env
        if env['version']!='8.0.45' or env['page_size']!=16384 or not env['file_per_table'] or env['checksum'] not in ('crc32','strict_crc32'):
            raise RuntimeError('unsupported fixture instance configuration')
        query("SET SESSION sql_mode='STRICT_TRANS_TABLES'")
        query("SET SESSION time_zone='+00:00'")
        env['sql_mode'] = 'STRICT_TRANS_TABLES'
        env['time_zone'] = '+00:00'
        query(f'CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci')
        query(f'USE `{database}`')
        for name, exprs in cases:
            query(f'CREATE TABLE `{name}` (id INT NOT NULL PRIMARY KEY, doc JSON NULL, note VARCHAR(32) NULL) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
            # Append another ordinary column to detect JSON boundary mistakes.
            for start in range(0,len(exprs),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join(f"({i},{e},'after-json')" for i,e in enumerate(exprs[start:start+100],start+1)))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                expected = [json.loads(x) for x in query(f"SELECT JSON_OBJECT('id',id,'sql_null',doc IS NULL,'text',CAST(doc AS CHAR CHARACTER SET utf8mb4),'type',JSON_TYPE(doc),'depth',JSON_DEPTH(doc),'bytes',JSON_STORAGE_SIZE(doc),'note',note) FROM `{name}` ORDER BY id")]
                if name == 'json_opaque':
                    types = [json.loads(x) for x in query("SELECT JSON_OBJECT('id',id,'types',JSON_ARRAY(" + ','.join("JSON_TYPE(JSON_EXTRACT(doc,'$["+str(i)+"]'))" for i in range(4)) + ")) FROM `json_opaque` ORDER BY id")]
                    (out/'json_opaque.types.json').write_text(json.dumps(types,indent=2)+'\n')
                index = json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND i.NAME='PRIMARY'")[0])
                ddl = '\n'.join(query(f'SHOW CREATE TABLE `{name}`'))
                (out/(name+'.sql')).write_text(ddl+'\n', encoding='utf-8')
                data = (Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:
                query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            schema = dict(columns=[dict(name='id',type='INT'),dict(name='doc',type='JSON',nullable=True),dict(name='note',type='VARCHAR',nullable=True,max_chars=32)],primary_key='id',**index)
            (out/(name+'.json')).write_text(json.dumps(schema,indent=2)+'\n')
            save(name+'.expected.json.gz',expected)
            manifest['cases'].append(dict(name=name,rows=len(expected),bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),**index))
        manifest['status']='captured'
    finally:
        (out/'generate.sql.gz').write_bytes(gzip.compress(('\n'.join(log)+'\n').encode(),mtime=0))
        (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
        proc.stdin.close()
        try: proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.terminate()
            proc.wait(timeout=15)
        proc.stdout.close()
    print(json.dumps(manifest))


if __name__=='__main__':
    main()
