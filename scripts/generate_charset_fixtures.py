#!/usr/bin/env python3
"""Capture character-set values in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
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
    database = 'innodb_reader_charset_' + uuid.uuid4().hex[:12]
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

    def encoded(data,cs):
        return "CONVERT(X'"+data.hex()+"' USING "+cs+")"

    def textcol(name,typ,cs,n=None):
        d=dict(name=name,type=typ,nullable=True)
        if cs!='utf8mb4': d['charset']=cs
        if n is not None:d['max_chars']=n
        return d

    collations=[('utf8mb4', 'utf8mb4_0900_ai_ci'),('utf8mb4','utf8mb4_general_ci'),('utf8mb4','utf8mb4_bin'),
        ('utf8mb3','utf8mb3_general_ci'),('utf8mb3','utf8mb3_bin'),('ascii','ascii_general_ci'),('ascii','ascii_bin'),('latin1','latin1_swedish_ci'),('latin1','latin1_bin')]
    special={'utf8mb4':'😀'.encode(),'utf8mb3':'中'.encode(),'ascii':b'Z','latin1':b'\x80'}
    labels={'utf8mb4':'😀','utf8mb3':'中','ascii':'Z','latin1':'€'}
    cases=[]
    for cs,coll in collations:
        cols=[textcol('c','CHAR',cs,5),textcol('v','VARCHAR',cs,256),textcol('t','TEXT',cs),textcol('cz','CHAR',cs,0),
              dict(name='bz',type='BINARY',nullable=True,max_bytes=0),textcol('vz','VARCHAR',cs,0),dict(name='vbz',type='VARBINARY',nullable=True,max_bytes=0),
              dict(name='e',type='ENUM',nullable=True,enum_values=['','a',labels[cs]],**({'charset':cs} if cs!='utf8mb4' else {})),
              dict(name='s',type='SET',nullable=True,set_values=['a',labels[cs]],**({'charset':cs} if cs!='utf8mb4' else {}))]
        rows=[]
        for data in [None,b'',b'a',b'a ',b'X\t',special[cs]]:
            if data is None:rows.append(['NULL']*len(cols))
            else:rows.append([encoded(data,cs)]*3+["''"]*4+['3','3'])
        cases.append(('charset_'+coll,cs,coll,cols,rows))
    for cs,coll in [('utf8mb4','utf8mb4_0900_ai_ci'),('utf8mb3','utf8mb3_general_ci'),('ascii','ascii_general_ci'),('latin1','latin1_swedish_ci')]:
        cols=[textcol('c'+str(n),'CHAR',cs,n) for n in [0,1,63,64,85,86,127,128,255]]+[textcol('v'+str(n),'VARCHAR',cs,n) for n in [0,1,63,64,85,86,127,128,255,256]]
        rows=[['NULL']*len(cols),["''"]*len(cols),[encoded(b'a'*c['max_chars'],cs) for c in cols],[encoded(special[cs]*c['max_chars'],cs) for c in cols]]
        cases.append(('widths_'+cs,cs,coll,cols,rows))
        cols=[textcol('tiny','TINYTEXT',cs),textcol('doc','MEDIUMTEXT',cs),textcol('longdoc','LONGTEXT',cs)]
        rows=[['NULL']*3,[encoded(special[cs]*40,cs),encoded(special[cs]*20000,cs),encoded(special[cs]*21000,cs)]]
        cases.append(('external_'+cs,cs,coll,cols,rows))
    for cs,count,coll in [('ascii',128,'ascii_general_ci'),('latin1',256,'latin1_swedish_ci')]:
        cols=[textcol('c','CHAR',cs,1),textcol('v','VARCHAR',cs,1),textcol('t','TINYTEXT',cs)]
        cases.append(('bytes_'+cs,cs,coll,cols,[[encoded(bytes([i]),cs)]*3 for i in range(count)]))
    cols=[textcol('c','CHAR','utf8mb3',5),textcol('v','VARCHAR','utf8mb3',32)]
    cases.append(('charset_utf8_alias','utf8','utf8_general_ci',cols,[[encoded(b'a','utf8')]*2,[encoded('中'.encode(),'utf8')]*2]))
    cols=[textcol('c','CHAR','latin1',16),textcol('v','VARCHAR','latin1',256),textcol('t','TEXT','latin1')]
    cases.append(('charset_tree','latin1','latin1_bin',cols,[[encoded(b'\x80'+str(i).encode(),'latin1'),encoded(b'\x80'*200,'latin1'),encoded(b'\x81\x9d'*100,'latin1')] for i in range(600)]))

    def declaration(c):
        typ=c['type']
        if typ in ('CHAR','VARCHAR'):typ+='('+str(c['max_chars'])+')'
        elif typ in ('BINARY','VARBINARY'):typ+='('+str(c['max_bytes'])+')'
        elif typ in ('ENUM','SET'):typ+='('+','.join("'"+v+"'" for v in c['enum_values' if typ=='ENUM' else 'set_values'])+')'
        return '`'+c['name']+'` '+typ+' NULL'

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
        for name,cs,coll,columns,rows in cases:
            columns=[dict(name='id',type='INT')]+columns+[dict(name='note',type='VARCHAR',max_chars=32,nullable=True)]
            ddl=', '.join(declaration(c) for c in columns[1:-1])
            query(f'CREATE TABLE `{name}` (id INT NOT NULL PRIMARY KEY, {ddl}, note VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NULL) ENGINE=InnoDB ROW_FORMAT=DYNAMIC DEFAULT CHARSET={cs} COLLATE={coll}')
            for start in range(0,len(rows),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join('('+str(i)+','+','.join(row)+",'after-charset')" for i,row in enumerate(rows[start:start+100],start+1)))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                cells=[]
                for c in columns[1:-1]:
                    col='`'+c['name']+'`'
                    txt='NULL' if c['type'] in ('BINARY','VARBINARY') else 'CONVERT('+col+' USING utf8mb4)'
                    cells.append("JSON_OBJECT('hex',HEX("+col+"),'text',"+txt+",'chars',CHAR_LENGTH("+col+"),'number',"+(col+'+0' if c['type'] in ('ENUM','SET') else 'NULL')+")")
                select="SELECT JSON_OBJECT('id',id,'cells',JSON_ARRAY("+','.join(cells)+"),'note',note) FROM `"+name+"` ORDER BY id"
                expected=[json.loads(x) for x in query(select)]
                query("SET SESSION sql_mode='STRICT_TRANS_TABLES,PAD_CHAR_TO_FULL_LENGTH'")
                padded=[json.loads(x) for x in query(select)]
                query("SET SESSION sql_mode='STRICT_TRANS_TABLES'")
                for row,pad in zip(expected,padded):
                    for cell,full in zip(row['cells'],pad['cells']):cell['full_hex']=full['hex'];cell['full_text']=full['text']
                index=json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND i.NAME='PRIMARY'")[0])
                (out/(name+'.sql')).write_text('\n'.join(query(f'SHOW CREATE TABLE `{name}`'))+'\n',encoding='utf-8')
                data=(Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            schema=dict(columns=columns,primary_key='id',**index)
            (out/(name+'.json')).write_text(json.dumps(schema,ensure_ascii=False,indent=2)+'\n')
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
