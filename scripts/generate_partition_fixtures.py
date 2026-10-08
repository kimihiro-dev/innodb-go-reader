#!/usr/bin/env python3
"""Capture partition collections in a fresh database, leaving the user instance running."""
import argparse, getpass, gzip, hashlib, json, os, subprocess, uuid
from pathlib import Path
from datetime import datetime, timezone

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--mysql', required=True)
p.add_argument('--socket', required=True)
p.add_argument('--out', required=True)
p.add_argument('--resume', action='store_true')
a = p.parse_args()
out = Path(a.out)
if out.exists() != a.resume: p.error('new output must not exist; --resume requires incomplete output')
env = os.environ.copy()
env['MYSQL_PWD'] = getpass.getpass('MySQL password: ')
client = subprocess.Popen([a.mysql, '--no-defaults', '-uroot', '--socket='+a.socket,
    '--batch', '--raw', '--skip-column-names', '--unbuffered'], stdin=subprocess.PIPE,
    stdout=subprocess.PIPE, text=True, env=env)
del env['MYSQL_PWD']
out.mkdir(parents=True, exist_ok=a.resume)
log = []
db = 'innodb_reader_partition_' + uuid.uuid4().hex[:12]
manifest = {'database': db, 'generated_at': datetime.now(timezone.utc).isoformat(), 'status': 'incomplete', 'cases': []}
if a.resume:
    manifest=json.loads((out/'manifest.json').read_text());assert manifest['status']=='incomplete'
    db=manifest['database'];log=gzip.decompress((out/'writer.sql.gz').read_bytes()).decode().splitlines()

def q(sql):
    log.append(sql+';')
    token = 'end'+uuid.uuid4().hex
    client.stdin.write(sql+";\nSELECT '"+token+"';\n")
    client.stdin.flush()
    rows = []
    while True:
        line = client.stdout.readline()
        if not line: raise RuntimeError('mysql exited')
        line = line.rstrip('\n')
        if line == token: return rows
        rows.append(line)

def save(name, value):
    data = value if isinstance(value, bytes) else (json.dumps(value, ensure_ascii=False, indent=2)+'\n').encode()
    (out/name).write_bytes(gzip.compress(data, mtime=0))

def checkpoint(): (out/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')

def capture(table, name, supported=True):
    parts = [json.loads(x) for x in q("SELECT JSON_OBJECT('name',PARTITION_NAME,'sub',SUBPARTITION_NAME,'ordinal',PARTITION_ORDINAL_POSITION,'subordinal',SUBPARTITION_ORDINAL_POSITION,'method',PARTITION_METHOD,'expression',PARTITION_EXPRESSION,'description',PARTITION_DESCRIPTION) FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA='"+db+"' AND TABLE_NAME='"+table+"' ORDER BY PARTITION_ORDINAL_POSITION,SUBPARTITION_ORDINAL_POSITION")]
    expected = [json.loads(x) for x in q(f'SELECT JSON_ARRAY(id,n,payload) FROM `{table}` ORDER BY id,n')]
    per = {}
    for part in parts:
        key = part['sub'] or part['name']
        per[key] = [json.loads(x) for x in q(f'SELECT JSON_ARRAY(id,n,payload) FROM `{table}` PARTITION (`{key}`) ORDER BY id,n')]
    ddl = q(f'SHOW CREATE TABLE `{table}`')
    q(f'FLUSH TABLES `{table}` FOR EXPORT')
    try:
        files = []
        for part in parts:
            physical = table+'#p#'+part['name']
            if part['sub']: physical += '#sp#'+part['sub']
            raw = (Path(info['datadir'])/db/(physical+'.ibd')).read_bytes()
            key = part['sub'] or part['name']
            filename = name+'-'+key+'.partition.gz'
            save(filename, raw)
            files.append({'partition': key, 'file': filename, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest(), 'rows': len(per[key])})
        sql_tables = [json.loads(x) for x in q("SELECT JSON_OBJECT('name',NAME,'table_id',TABLE_ID,'space',SPACE,'format',ROW_FORMAT) FROM information_schema.INNODB_TABLES WHERE NAME LIKE '"+db+'/'+table+"#p#%' ORDER BY NAME")]
        sql_indexes = [json.loads(x) for x in q("SELECT JSON_OBJECT('table',t.NAME,'name',i.NAME,'id',i.INDEX_ID,'page',i.PAGE_NO,'space',i.SPACE) FROM information_schema.INNODB_INDEXES i JOIN information_schema.INNODB_TABLES t ON i.TABLE_ID=t.TABLE_ID WHERE t.NAME LIKE '"+db+'/'+table+"#p#%' ORDER BY t.NAME,i.INDEX_ID")]
    finally: q('UNLOCK TABLES')
    save(name+'.expected.json.gz', {'rows': expected, 'partitions': per})
    save(name+'.sql-metadata.json.gz', {'partitions': parts, 'tables': sql_tables, 'indexes': sql_indexes, 'ddl': ddl})
    manifest['cases'].append({'name':name,'table':table,'supported':supported,'files':files,'rows':len(expected)})
    checkpoint()
    print(name, len(files), 'files', len(expected), 'rows', flush=True)

try:
    info = json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0])
    manifest['environment'] = info
    assert info['version']=='8.0.45' and info['page_size']==16384 and info['file_per_table']==1 and info['checksum'] in ('crc32','strict_crc32')
    if not a.resume: q(f'CREATE DATABASE `{db}`')
    q(f'USE `{db}`');q('SET SESSION sql_require_primary_key=OFF');checkpoint()
    definitions = [
        ('ranges', 'RANGE (n) (PARTITION p0 VALUES LESS THAN (10),PARTITION p1 VALUES LESS THAN (20),PARTITION p2 VALUES LESS THAN MAXVALUE)', 'DYNAMIC', True, ''),
        ('lists', 'LIST (n) (PARTITION p0 VALUES IN (0,1),PARTITION p1 VALUES IN (2,3),PARTITION p2 VALUES IN (4,5))','DYNAMIC',True,''),
        ('hashes','HASH (n) PARTITIONS 3','DYNAMIC',True,''),
        ('keys_table','KEY (n) PARTITIONS 3','DYNAMIC',True,''),
        ('compact_rows','RANGE (n) (PARTITION p0 VALUES LESS THAN (10),PARTITION p1 VALUES LESS THAN MAXVALUE)','COMPACT',True,''),
        ('hidden_rows','HASH (n) PARTITIONS 2','DYNAMIC',False,''),
        ('virtual_rows','HASH (n) PARTITIONS 2','DYNAMIC',True,', v INT GENERATED ALWAYS AS (n+1) VIRTUAL'),
        ('sub_rows','RANGE(n) SUBPARTITION BY HASH(id) SUBPARTITIONS 2 (PARTITION p0 VALUES LESS THAN (10),PARTITION p1 VALUES LESS THAN MAXVALUE)','DYNAMIC',True,'')]
    for table, partition, row_format, pk, extra in definitions:
        if any(c['name']==table for c in manifest['cases']): continue
        q(f'CREATE TABLE `{table}` (id BIGINT NOT NULL,n INT NOT NULL,payload LONGTEXT{extra}'+(',PRIMARY KEY(id,n)' if pk else '')+f',KEY s(n)) ENGINE=InnoDB ROW_FORMAT={row_format} PARTITION BY {partition}')
        if table=='ranges': capture(table,'range_empty')
        count = 1000 if table=='ranges' else 30
        for start in range(0,count,100):
            values = []
            for i in range(start,min(start+100,count)):
                n = i % (4 if table=='lists' else 20)
                payload = "REPEAT('L',20000)" if i%101==0 else ("NULL" if i%7==0 else "CONCAT('row',"+str(i)+")")
                values.append(f'({i},{n},{payload})')
            q(f'INSERT INTO `{table}` (id,n,payload) VALUES '+','.join(values))
        capture(table,table,table!='sub_rows')
    completed={c['name'] for c in manifest['cases']}
    if 'range_rebuilt' not in completed:
        q('ALTER TABLE ranges REBUILD PARTITION p0');capture('ranges','range_rebuilt')
    if 'range_exchanged' not in completed:
        q('CREATE TABLE swap_rows LIKE ranges');q('ALTER TABLE swap_rows REMOVE PARTITIONING')
        q("INSERT INTO swap_rows VALUES (2001,1,'exchanged'),(2002,2,'after exchange')")
        q('ALTER TABLE ranges EXCHANGE PARTITION p0 WITH TABLE swap_rows')
        capture('ranges','range_exchanged')
    if 'range_rebuilt_nonanchor' not in completed:
        q('ALTER TABLE ranges REBUILD PARTITION p1');capture('ranges','range_rebuilt_nonanchor')
    manifest['status']='captured'
finally:
    save('writer.sql.gz', '\n'.join(log).encode());checkpoint()
    client.stdin.close();client.wait(timeout=30)
