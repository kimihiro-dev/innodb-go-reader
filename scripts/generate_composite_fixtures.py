#!/usr/bin/env python3
"""Capture composite integer primary keys in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
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
    database = 'innodb_reader_composite_' + uuid.uuid4().hex[:12]
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

    def col(name,kind,**kw):return dict(name=name,type=kind,**kw)
    lesson=[col('text','VARCHAR',max_chars=32,nullable=True),col('b','BIGINT',unsigned=True),col('n','INT',nullable=True),col('a','SMALLINT'),col('c','MEDIUMINT'),col('tail','VARCHAR',max_chars=32,nullable=True)]
    rows=[["'before'",str(b),'NULL',str(a),str(c),"'after'"] for a,b,c in [(0,0,0),(-32768,18446744073709551615,8388607),(-32768,0,-8388608),(0,1,-1),(0,0,1),(0,0,-1),(32767,0,0),(-32768,0,0)]]
    cases=[('composite_lesson',lesson,['a','b','c'],rows),('composite_empty',lesson,['a','b','c'],[])]
    kinds=[('TINYINT',1),('SMALLINT',2),('MEDIUMINT',3),('INT',4),('BIGINT',8)]
    cols=[col('k'+str(i*2+u),kind,unsigned=bool(u)) for i,(kind,width) in enumerate(kinds) for u in (0,1)]
    keys=[c['name'] for c in reversed(cols)]
    rows=[]
    for sign in (-1,0,1):
        row=[]
        for kind,width in kinds:row += [str(-(1<<(width*8-1)) if sign<0 else (1<<(width*8-1))-1 if sign>0 else 0),str((1<<(width*8))-1 if sign>0 else 0)]
        rows.append(row)
    cases.append(('composite_types',cols,keys,rows))
    cols=[col('a','INT'),col('doc','MEDIUMTEXT',nullable=True),col('c','BIGINT',unsigned=True),col('b','TINYINT')]
    cases.append(('composite_lob',cols,['a','b','c'],[['0',"REPEAT('中',20000)",'18446744073709551615','-1'],['0','NULL','0','-1'],['0',"''",'0','0']]))
    cols=[col('payload','VARCHAR',max_chars=256,nullable=True),col('c','INT'),col('a','SMALLINT'),col('n','INT',nullable=True),col('b','SMALLINT')]
    rows=[["REPEAT('x',180)",str(i%10),str(i//200),'NULL' if i%3==0 else str(i),str(i//10%20)] for i in range(600)]
    import random
    random.Random(8045).shuffle(rows)
    cases.append(('composite_tree',cols,['a','b','c'],rows))
    cols=[col('k'+str(i),'BIGINT',unsigned=bool(i%2)) for i in range(16)]+[col('payload','VARCHAR',max_chars=1500,nullable=True)]
    rows=[['0']*15+[str(i),"REPEAT('d',1200)"] for i in range(3000)]
    cases.append(('composite_deep',cols,[c['name'] for c in cols[:16]],rows))

    def declaration(c):
        typ=c['type']
        if typ=='VARCHAR':typ+='('+str(c['max_chars'])+')'
        if c.get('unsigned'):typ+=' UNSIGNED'
        return '`'+c['name']+'` '+typ+(' NULL' if c.get('nullable') else ' NOT NULL')
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
        for name,columns,keys,rows in cases:
            key_sql=','.join('`'+k+'`' for k in keys)
            query(f'CREATE TABLE `{name}` ('+', '.join(declaration(c) for c in columns)+', PRIMARY KEY ('+key_sql+')) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
            for start in range(0,len(rows),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join('('+','.join(row)+')' for row in rows[start:start+100]))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                select='SELECT JSON_ARRAY('+','.join('`'+c['name']+'`' for c in columns)+f') FROM `{name}` ORDER BY '+key_sql
                expected=[json.loads(x) for x in query(select)]
                index=json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND i.NAME='PRIMARY'")[0])
                (out/(name+'.sql')).write_text('\n'.join(query(f'SHOW CREATE TABLE `{name}`'))+'\n',encoding='utf-8')
                data=(Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            (out/(name+'.json')).write_text(json.dumps(dict(columns=columns,primary_keys=keys,**index),indent=2)+'\n')
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
