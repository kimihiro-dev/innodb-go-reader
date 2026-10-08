#!/usr/bin/env python3
"""Capture current LOB updates, JSON partial changes and retained/purged versions."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid
from datetime import datetime, timezone


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--mysql',required=True)
    p.add_argument('--socket',required=True)
    p.add_argument('--out',required=True)
    args=p.parse_args();out=Path(args.out).resolve()
    if out.exists():p.error('output must not exist')
    out.mkdir(parents=True)
    database='innodb_reader_lob_updates_'+uuid.uuid4().hex[:12]
    clients={};logs={'writer':[],'holder':[]}
    for role in logs:
        clients[role]=subprocess.Popen([args.mysql,'--no-defaults','-uroot','--socket='+args.socket,'--default-character-set=utf8mb4','--max-allowed-packet=128M','--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,encoding='utf-8',env=os.environ.copy())
    def query(sql,role='writer'):
        logs[role].append(sql+';');client=clients[role];marker='__end_'+uuid.uuid4().hex
        client.stdin.write(sql+";\nSELECT '"+marker+"';\n");client.stdin.flush();rows=[]
        while True:
            line=client.stdout.readline()
            if not line:raise RuntimeError(role+' mysql exited')
            line=line.rstrip('\n')
            if line==marker:return rows
            rows.append(line)
    def col(name,kind,**kw):return dict(name=name,type=kind,**kw)
    def save(name,data):
        if not isinstance(data,bytes):data=(json.dumps(data,ensure_ascii=False,indent=2)+'\n').encode()
        (out/name).write_bytes(gzip.compress(data,mtime=0))
    def declaration(c):
        typ=c['type']
        if typ=='VARCHAR':typ+='('+str(c['max_chars'])+')'+(' COLLATE '+c['collation'] if c.get('collation') else '')
        if typ=='VARBINARY':typ+='('+str(c['max_bytes'])+')'
        return '`'+c['name']+'` '+typ+(' NULL' if c.get('nullable') else ' NOT NULL')
    cases=[]
    cols=[col('id','INT'),col('t','LONGTEXT',nullable=True),col('b','LONGBLOB',nullable=True),col('n','INT')]
    rows=[[str(i),"REPEAT('😀界',10000)","REPEAT(UNHEX('00FF8041'),18000)",'0'] for i in range(1,5)]
    updates=[('inherited',["UPDATE `{t}` SET id=11,n=1 WHERE id=1"]),
             ('replace',["UPDATE `{t}` SET t=REPEAT('更新😀',18000),b=REPEAT(UNHEX('FE0001'),40000) WHERE id=2"]),
             ('shrink',["UPDATE `{t}` SET t='short',b=UNHEX('00FF') WHERE id=2","UPDATE `{t}` SET t=NULL,b='' WHERE id=3"]),
             ('regrow',["UPDATE `{t}` SET t=REPEAT('跨页😀',24000),b=REPEAT(UNHEX('00FF'),80000) WHERE id=2"]),
             ('deleted',["DELETE FROM `{t}` WHERE id=4"])]
    cases.append(('values',cols,'PRIMARY KEY(id)',['id'],rows,updates))
    cols=[col('id','INT'),col('j','JSON',nullable=True),col('n','INT')]
    rows=[['1',"JSON_OBJECT('a',REPEAT('😀界',9000),'b',REPEAT('B',800),'c',JSON_ARRAY(1,CAST('12345678901234567890.123' AS DECIMAL(30,3)),NULL))",'0'],
          ['2',"JSON_OBJECT('a',REPEAT('x',350000),'b','tail','c',JSON_ARRAY(1,2,3))",'0'],
          ['3',"JSON_OBJECT('a',REPEAT('q',40),'b',REPEAT('r',40),'c',JSON_ARRAY('long nested string','another string'))",'0']]
    updates=[('small',["UPDATE `{t}` SET j=JSON_SET(j,'$.c[0]',2) WHERE id=1"]),
             ('large',["UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT('界😀',9000)) WHERE id=1","UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT('y',350000)) WHERE id=2"]),
             ('repeated',["UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT(CHAR("+str(65+i)+"),350000)) WHERE id=2" for i in range(14)]),
             ('holes',["UPDATE `{t}` SET j=JSON_REMOVE(JSON_SET(j,'$.a',REPEAT('界',1000)),'$.b') WHERE id=1","UPDATE `{t}` SET j=JSON_SET(j,'$.a','s','$.c[0]','x') WHERE id=3"]),
             ('reuse',["UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT('z',20),'$.b',REPEAT('v',35)) WHERE id=3","UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT('复用😀',2000)) WHERE id=1"]),
             ('grow',["UPDATE `{t}` SET j=JSON_SET(j,'$.a',REPEAT('g',400000)) WHERE id=2"]),
             ('deleted',["DELETE FROM `{t}` WHERE id=1"])]
    cases.append(('documents',cols,'PRIMARY KEY(id)',['id'],rows,updates))
    manifest=dict(database=database,generated_at=datetime.now(timezone.utc).isoformat(),cases=[],status='incomplete')
    try:
        env=json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
        if env['version']!='8.0.45' or env['page_size']!=16384 or not env['file_per_table'] or env['checksum'] not in ('crc32','strict_crc32'):raise RuntimeError('unsupported instance')
        query("SET SESSION sql_mode='STRICT_TRANS_TABLES'")
        query("SET SESSION time_zone='+00:00'")
        query('SET SESSION sql_require_primary_key=OFF')
        query('SET SESSION sql_generate_invisible_primary_key=OFF')
        query('SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ','holder')
        query(f'CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci')
        query(f'USE `{database}`')
        with tempfile.TemporaryDirectory(prefix='innodb-changes-') as temp:
            temp=Path(temp);probe=temp/'probe';snapshot=temp/'table.ibd'
            subprocess.run(['go','build','-o',str(probe),'./examples/changes'],env=dict(os.environ,GOCACHE='/tmp/innodb-go-build-cache'),check=True)
            for table,columns,indexes,keys,rows,updates in cases:
                query(f'CREATE TABLE `{table}` ('+','.join(declaration(c) for c in columns)+(','+indexes if indexes else '')+') ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
                for start in range(0,len(rows),100):query(f'INSERT INTO `{table}` VALUES '+','.join('('+','.join(row)+')' for row in rows[start:start+100]))
                def capture(phase,require_purged=False):
                    name=table+'_'+phase
                    for attempt in range(30):
                        query(f'FLUSH TABLES `{table}` FOR EXPORT')
                        try:
                            expr=','.join('HEX(`'+c['name']+'`)' if c['type']=='VARBINARY' or c['type'].endswith('BLOB') else '`'+c['name']+'`' for c in columns)
                            order=','.join('`'+k+'`'+(' DESC' if next(c for c in columns if c['name']==k).get('descending') else ' ASC') for k in (keys or []))
                            expected=query('SELECT JSON_ARRAY('+expr+f') FROM `{table}`'+(' ORDER BY '+order if keys else ''))
                            identities=[json.loads(x) for x in query("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{table}' ORDER BY i.INDEX_ID")]
                            json_storage=[json.loads(x) for x in query(f'SELECT JSON_ARRAY(id,JSON_STORAGE_SIZE(j),JSON_STORAGE_FREE(j)) FROM `{table}` ORDER BY id')] if table=='documents' else []
                            ddl='\n'.join(query(f'SHOW CREATE TABLE `{table}`'))+'\n'
                            data=(Path(env['datadir'])/database/(table+'.ibd')).read_bytes()
                        finally:query('UNLOCK TABLES')
                        snapshot.write_bytes(data)
                        run=subprocess.run([str(probe),str(snapshot)],capture_output=True,text=True)
                        if run.returncode:
                            (out/(name+'.failed.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
                            raise RuntimeError(run.stderr)
                        report=json.loads(run.stdout)
                        if not require_purged or report['DeleteMarked']==0:break
                        time.sleep(1)
                    else:raise RuntimeError('purge did not finish within 30 captures: '+table)
                    if report['Rows']!=len(expected):raise RuntimeError('current row count mismatch '+name)
                    # LOB histories may exist without any delete-marked clustered record.
                    index=next(i for i in identities if i['type']&1)
                    layout=dict(columns=columns,**{k:index[k] for k in ('root_page','index_id','space_id')})
                    if keys is None:layout['clustered_key']=dict(name='PRIMARY',hidden_row_id=True)
                    elif table=='unique':layout['clustered_key']=dict(name='u',columns=keys)
                    elif len(keys)==1:layout['primary_key']=keys[0]
                    else:layout['primary_keys']=keys
                    save(name+'.ibd.gz',data);save(name+'.expected.json.gz',('['+','.join(expected)+']\n').encode());save(name+'.indexes.json.gz',identities)
                    (out/(name+'.json')).write_text(json.dumps(layout,indent=2)+'\n')
                    (out/(name+'.sql')).write_text(ddl)
                    manifest['cases'].append(dict(name=name,table=table,phase=phase,rows=len(expected),unordered=keys is None,bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),physical=report,json_storage=json_storage,**{k:index[k] for k in ('root_page','index_id','space_id')}))
                    print(name,report,flush=True)
                capture('before')
                # Global read view retains old versions without target-table MDL.
                query('START TRANSACTION WITH CONSISTENT SNAPSHOT','holder')
                for phase,statements in updates:
                    for sql in statements:
                        query('START TRANSACTION')
                        query(sql.format(t=table))
                        query('COMMIT')
                    capture(phase)
                    if phase=='reuse':
                        query('COMMIT','holder')
                        time.sleep(2)
                        capture('history_purged',True)
                        query('START TRANSACTION WITH CONSISTENT SNAPSHOT','holder')
                query('COMMIT','holder')
                time.sleep(2)
                capture('purged',True)
            manifest['status']='captured'
    finally:
        for role,client in clients.items():
            save(role+'.sql.gz',('\n'.join(logs[role])+'\n').encode())
            client.stdin.close()
            try:client.wait(timeout=10)
            except subprocess.TimeoutExpired:client.terminate();client.wait(timeout=10)
            client.stdout.close()
        (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')

if __name__=='__main__':main()
