#!/usr/bin/env python3
"""Capture typed ASC/DESC primary keys in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
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
    database = 'innodb_reader_keys_' + uuid.uuid4().hex[:12]
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
    def add(name,columns,keys,rows): cases.append((name,columns,keys,rows))
    # Every selected collation, CHAR and VARCHAR, both directions. The second
    # key distinguishes PAD SPACE equivalent strings without violating uniqueness.
    for charset in ('utf8mb4','utf8mb3','ascii','latin1'):
        for kind in ('CHAR','VARCHAR'):
            for desc in (False,True):
                c=col('k',kind,max_chars=80,charset='' if charset=='utf8mb4' else charset,collation=charset+'_bin',descending=desc)
                values=['','a','a ','a  ','a\x00','a\x1f','a!','A','b','z']
                if charset!='ascii': values+=['é','ÿ']
                if charset=='latin1': values+=['€']
                if charset.startswith('utf8'): values+=['中','文']
                if charset=='utf8mb4': values+=['😀']
                rows=[]
                for i,v in enumerate(values):
                    raw=v.encode('cp1252' if charset=='latin1' else 'utf-8')
                    rows.append(["'before'",str(i),"CONVERT(X'"+raw.hex()+"' USING "+charset+")",'NULL'])
                add('keys_'+charset+'_'+kind.lower()+('_desc' if desc else '_asc'),[col('before','VARCHAR',max_chars=12),col('tie','INT',descending=not desc),c,col('n','INT',nullable=True)],['k','tie'],rows)
    for kind in ('BINARY','VARBINARY'):
        for desc in (False,True):
            values=['','00','20','61','6100','6120','ff','00ff']
            rows=[["X'"+v+"'",str(i)] for i,v in enumerate(values)]
            add('keys_'+kind.lower()+('_desc' if desc else '_asc'),[col('k',kind,max_bytes=8,descending=desc),col('tie','INT')],['k','tie'],rows)
    specifications=[(col('k','DECIMAL',precision=65,scale=30),['-99999999999999999999999999999999999.999999999999999999999999999999','-1.000000000000000000000000000001','0','0.000000000000000000000000000001','99999999999999999999999999999999999.999999999999999999999999999999']),
      (col('k','BIT',bit_length=64),['0','1','9223372036854775808','18446744073709551615']),
      (col('k','DATE'),["'0000-00-00'","'1000-01-01'","'2024-02-29'","'9999-12-31'"]),
      (col('k','YEAR'),['0','1901','2024','2155'])]
    for fsp in range(7):
        suffix='.'+'1'*fsp if fsp else ''
        specifications += [(col('k','TIME',fsp=fsp),["'-838:59:58"+suffix+"'","'-00:00:01"+suffix+"'","'00:00:00'","'00:00:00"+suffix+"'" if fsp else "'00:00:01'","'838:59:58"+suffix+"'"]),
          (col('k','DATETIME',fsp=fsp),["'0000-00-00 00:00:00'","'1000-01-01 00:00:00"+suffix+"'","'9999-12-31 23:59:58"+suffix+"'"]),
          (col('k','TIMESTAMP',fsp=fsp),["'0000-00-00 00:00:00'","'1970-01-01 00:00:01"+suffix+"'","'2038-01-19 03:14:07"+suffix+"'"])]
    for c,values in specifications:
        for desc in (False,True):
            c=dict(c,descending=desc)
            name='keys_'+c['type'].lower()+str(c.get('fsp',''))+('_desc' if desc else '_asc')
            add(name,[c],['k'],[[v] for v in values])
    cols=[col('doc','MEDIUMTEXT',nullable=True),col('tie','INT',descending=True),col('k','VARBINARY',max_bytes=768),col('text','VARCHAR',max_chars=32,collation='utf8mb4_bin',descending=True)]
    add('keys_lesson',cols,['text','k','tie'],[["REPEAT('中',20000)",'1',"REPEAT(X'61',128)","'a'"],['NULL','2',"X''","'a '"],["'short'",'3',"REPEAT(X'ff',256)","'中'"]])
    add('keys_empty',cols,['text','k','tie'],[])
    cols=[col('payload','VARCHAR',max_chars=1500,nullable=True),col('tie','INT',descending=True),col('k','VARCHAR',max_chars=192,collation='utf8mb4_bin',descending=True),col('binary','VARBINARY',max_bytes=768)]
    rows=[["REPEAT('x',1200)",str(i%5),"CONCAT(LPAD("+str(i//50)+",4,'0'),REPEAT('a',"+str([0,123,124,188][i%4])+"))","CONCAT(REPEAT(X'62',"+str([0,127,128,255,256,768][i%6])+"),X'')"] for i in range(3000)]
    # An extra decimal suffix guarantees unique tuples across the length cycle.
    for i,row in enumerate(rows): row[1]=str(i)
    import random
    random.Random(8045).shuffle(rows)
    add('keys_deep',cols,['k','binary','tie'],rows)

    # Repeat each matrix in a real multi-page tree, with a final unique tie key.
    for name,columns,keys,rows in list(cases):
        if name in ('keys_lesson','keys_empty','keys_deep'): continue
        treecols=columns+[col('sequence','INT',descending=True),col('payload','VARCHAR',max_chars=1500)]
        treerows=[list(rows[i%len(rows)])+[str(i),"REPEAT('p',1200)"] for i in range(300)]
        random.Random(8045).shuffle(treerows)
        add(name+'_tree',treecols,keys+['sequence'],treerows)
    add('keys_max_width',[col('k','VARBINARY',max_bytes=3072)],['k'],[["REPEAT(X'61',3072)"],["X''"]])

    def declaration(c):
        typ=c['type']
        if typ in ('VARCHAR','CHAR'):typ+='('+str(c['max_chars'])+') CHARACTER SET '+(c.get('charset') or 'utf8mb4')+' COLLATE '+c.get('collation','utf8mb4_0900_ai_ci')
        if typ in ('BINARY','VARBINARY'):typ+='('+str(c['max_bytes'])+')'
        if typ=='DECIMAL':typ+='('+str(c['precision'])+','+str(c['scale'])+')'
        if typ=='BIT':typ+='('+str(c['bit_length'])+')'
        if typ in ('TIME','DATETIME','TIMESTAMP'):typ+='('+str(c['fsp'])+')'
        if c.get('unsigned'):typ+=' UNSIGNED'
        return '`'+c['name']+'` '+typ+(' NULL' if c.get('nullable') else ' NOT NULL')
    def expression(c):
        n='`'+c['name']+'`'
        if c['type'] in ('BINARY','VARBINARY'):return 'HEX('+n+')'
        if c['type'] in ('DECIMAL','DATE','TIME','DATETIME','TIMESTAMP'):return 'CAST('+n+' AS CHAR)'
        if c['type']=='BIT':return 'CAST('+n+' AS UNSIGNED)'
        return n
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
            key_sql=','.join('`'+k+'`'+(' DESC' if next(c for c in columns if c['name']==k).get('descending') else ' ASC') for k in keys)
            query(f'CREATE TABLE `{name}` ('+', '.join(declaration(c) for c in columns)+', PRIMARY KEY ('+key_sql+')) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
            for start in range(0,len(rows),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join('('+','.join(row)+')' for row in rows[start:start+100]))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                select='SELECT JSON_ARRAY('+','.join(expression(c) for c in columns)+f') FROM `{name}` ORDER BY '+key_sql
                expected=[json.loads(x) for x in query(select)]
                index=json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND i.NAME='PRIMARY'")[0])
                (out/(name+'.sql')).write_text('\n'.join(query(f'SHOW CREATE TABLE `{name}`'))+'\n',encoding='utf-8')
                data=(Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            (out/(name+'.json')).write_text(json.dumps(dict(columns=columns,**({"primary_key":keys[0]} if len(keys)==1 else {"primary_keys":keys}),**index),indent=2)+'\n')
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
