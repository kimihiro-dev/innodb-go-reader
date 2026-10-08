#!/usr/bin/env python3
"""Capture clustered indexes without explicit primary keys in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
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
    database = 'innodb_reader_cluster_' + uuid.uuid4().hex[:12]
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
    cases=[]
    def add(name,cols,indexes,keys,rows):cases.append((name,cols,indexes,keys,rows))
    cols=[col('n','INT',nullable=True),col('text','VARCHAR',max_chars=32,nullable=True)]
    rows=[['7',"'重复'"],['NULL','NULL'],['7',"'重复'"],['-1',"''"]]
    add('rowid_lesson',cols,[],None,rows)
    add('rowid_empty',cols,[],None,[])
    add('rowid_nullable_unique',cols,['UNIQUE KEY u(n)'],None,[['NULL',"'same'"],['NULL',"'same'"],['1',"'one'"]])
    cols=[col('text','VARCHAR',max_chars=32),col('n','INT',nullable=True)]
    add('rowid_prefix_unique',cols,['UNIQUE KEY u(text(2))'],None,[["'abcd'",'NULL'],["'cdef'",'1']])
    cols=[col('n','INT',nullable=True),col('text','VARCHAR',max_chars=32,nullable=True),col('blob','MEDIUMBLOB',nullable=True)]
    add('rowid_lob',cols,[],None,[['1',"'blob'","REPEAT(X'00ff',30000)"],['1',"'blob'","REPEAT(X'00ff',30000)"],['NULL','NULL','NULL']])
    cols=[col('payload','VARCHAR',max_chars=2000,nullable=True)]+[col('n'+str(i),'INT',nullable=True) for i in range(9)]
    add('rowid_deep',cols,[],None,[["REPEAT('r',2000)"]+['NULL']*9 for i in range(12000)])
    cols=[col('text','VARCHAR',max_chars=32,nullable=True),col('id','BIGINT',unsigned=True)]
    add('unique_single',cols,['UNIQUE KEY u(id)'],['id'],[["'max'",'18446744073709551615'],['NULL','0'],["'one'",'1']])
    cols=[col('nullable','INT',nullable=True),col('b','INT'),col('a','INT')]
    rows=[['NULL','20','2'],['NULL','10','1'],['3','30','0']]
    add('unique_multiple',cols,['UNIQUE KEY nullable_first(nullable)','UNIQUE KEY z_chosen(b)','UNIQUE KEY a_later(a)'],['b'],rows)
    cols=[col('payload','VARCHAR',max_chars=1200,nullable=True),col('n','INT',descending=True),col('k','VARCHAR',max_chars=100,collation='utf8mb4_bin',descending=True)]
    add('unique_text_tree',cols,['UNIQUE KEY u(k DESC,n DESC)','KEY secondary_n(n)'],['k','n'],[["REPEAT('p',1100)",str(i),"CONCAT('中',LPAD("+str(i//5)+",4,'0'))"] for i in range(600)])
    cols=[col('id','INT'),col('n','INT',nullable=True)]
    add('primary_secondary',cols,['PRIMARY KEY(id)','KEY n_idx(n)'],['id'],[['3','NULL'],['1','5'],['2','5']])
    def declaration(c):
        typ=c['type']
        if typ=='VARCHAR':typ+='('+str(c['max_chars'])+')'+(' COLLATE '+c['collation'] if c.get('collation') else '')
        if c.get('unsigned'):typ+=' UNSIGNED'
        return '`'+c['name']+'` '+typ+(' NULL' if c.get('nullable') else ' NOT NULL')
    def expression(c):
        n='`'+c['name']+'`'
        return 'HEX('+n+')' if c['type'].endswith('BLOB') else n
    manifest = dict(database=database, generated_at=datetime.now(timezone.utc).isoformat(), cases=[], status='incomplete')
    try:
        env = json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0])
        manifest['environment'] = env
        if env['version']!='8.0.45' or env['page_size']!=16384 or not env['file_per_table'] or env['checksum'] not in ('crc32','strict_crc32'):
            raise RuntimeError('unsupported fixture instance configuration')
        query("SET SESSION sql_mode='STRICT_TRANS_TABLES'")
        query("SET SESSION time_zone='+00:00'")
        query("SET SESSION sql_require_primary_key=OFF")
        query("SET SESSION sql_generate_invisible_primary_key=OFF")
        env["sql_require_primary_key"]=False
        env["sql_generate_invisible_primary_key"]=False
        env['sql_mode'] = 'STRICT_TRANS_TABLES'
        env['time_zone'] = '+00:00'
        query(f'CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci')
        query(f'USE `{database}`')
        for name,columns,indexes,keys,rows in cases:
            key_sql=','.join('`'+k+'`'+(' DESC' if next(c for c in columns if c['name']==k).get('descending') else ' ASC') for k in (keys or []))
            query(f'CREATE TABLE `{name}` ('+', '.join(declaration(c) for c in columns)+(' ,'+','.join(indexes) if indexes else '')+') ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
            for start in range(0,len(rows),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join('('+','.join(row)+')' for row in rows[start:start+100]))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                select='SELECT JSON_ARRAY('+','.join(expression(c) for c in columns)+f') FROM `{name}`'+(' ORDER BY '+key_sql if keys else '')
                expected=[json.loads(x) for x in query(select)]
                index=json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND (i.TYPE & 1)=1")[0])
                identities=[json.loads(x) for x in query("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' ORDER BY i.INDEX_ID")]
                save(name+'.indexes.json.gz',identities)
                (out/(name+'.sql')).write_text('\n'.join(query(f'SHOW CREATE TABLE `{name}`'))+'\n',encoding='utf-8')
                data=(Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            (out/(name+'.json')).write_text(json.dumps(dict(columns=columns,**({'primary_key':'id'} if name=='primary_secondary' else {'clustered_key':dict(name='PRIMARY' if keys is None else 'z_chosen' if name=='unique_multiple' else 'u',**({'hidden_row_id':True} if keys is None else {'columns':keys}))}),**index),indent=2)+'\n')
            save(name+'.expected.json.gz',expected)
            manifest['cases'].append(dict(name=name,rows=len(expected),unordered=keys is None,bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),**index))
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
