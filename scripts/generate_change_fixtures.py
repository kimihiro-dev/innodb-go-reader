#!/usr/bin/env python3
"""Capture committed changes with and without retained delete marks in a new database."""
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
    database='innodb_reader_changes_'+uuid.uuid4().hex[:12]
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
    cols=[col('id','INT'),col('n','INT',nullable=True),col('v','VARCHAR',max_chars=1500,nullable=True)]
    rows=[[str(i),str(i),"REPEAT('a',127)"] for i in range(1,7)]
    updates=["UPDATE `{t}` SET n=-7,v=REPEAT('b',128) WHERE id=1","UPDATE `{t}` SET n=NULL,v=NULL WHERE id=2","DELETE FROM `{t}` WHERE id=3","UPDATE `{t}` SET id=40,v=REPEAT('长',400) WHERE id=4","DELETE FROM `{t}` WHERE id=5","INSERT INTO `{t}` VALUES (5,55,'reinserted')","UPDATE `{t}` SET n=NULL,v='' WHERE id=6"]
    cases.append(('lesson',cols,'PRIMARY KEY(id)',['id'],rows,updates))
    cols=[col('id','INT'),col('v','VARCHAR',max_chars=32,nullable=True)]
    cases.append(('empty',cols,'PRIMARY KEY(id)',['id'],[[str(i),"'gone'"] for i in range(20)],["DELETE FROM `{t}`"]))
    cols=[col('n','INT',nullable=True),col('v','VARCHAR',max_chars=300,nullable=True)]
    cases.append(('hidden',cols,'',None,[['1',"'same'"],['1',"'same'"],['2','NULL'],['3',"'long'"]],["DELETE FROM `{t}` WHERE n=1","UPDATE `{t}` SET n=NULL,v=REPEAT('x',256) WHERE n=2","UPDATE `{t}` SET v='' WHERE n=3"]))
    cols=[col('n','INT',descending=True),col('k','VARCHAR',max_chars=32,collation='utf8mb4_bin',descending=True),col('v','VARCHAR',max_chars=256,nullable=True)]
    cases.append(('unique',cols,'UNIQUE KEY u(k DESC,n DESC),KEY idx_n(n)',['k','n'],[['1',"'a'","'one'"],['2',"'a '","'two'"],['3',"'中'",'NULL'],['4',"'z'","'four'"]],["UPDATE `{t}` SET k='b',v=REPEAT('v',128) WHERE n=1","DELETE FROM `{t}` WHERE n=2","UPDATE `{t}` SET v='' WHERE n=3"]))
    cols=[col('id','BIGINT',descending=True),col('k','VARBINARY',max_bytes=255),col('v','VARCHAR',max_chars=1800,nullable=True)]
    rows=[[str(i),"UNHEX(LPAD(HEX("+str(i//10)+"),510,'0'))","REPEAT('t',1600)"] for i in range(4000)]
    cases.append(('tree',cols,'PRIMARY KEY(k,id DESC)',['k','id'],rows,["DELETE FROM `{t}` WHERE id<3990","UPDATE `{t}` SET v=REPEAT('s',32) WHERE id>=3990"]))
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
                            expr=','.join('HEX(`'+c['name']+'`)' if c['type']=='VARBINARY' else '`'+c['name']+'`' for c in columns)
                            order=','.join('`'+k+'`'+(' DESC' if next(c for c in columns if c['name']==k).get('descending') else ' ASC') for k in (keys or []))
                            expected=[json.loads(x) for x in query('SELECT JSON_ARRAY('+expr+f') FROM `{table}`'+(' ORDER BY '+order if keys else ''))]
                            identities=[json.loads(x) for x in query("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{table}' ORDER BY i.INDEX_ID")]
                            ddl='\n'.join(query(f'SHOW CREATE TABLE `{table}`'))+'\n'
                            data=(Path(env['datadir'])/database/(table+'.ibd')).read_bytes()
                        finally:query('UNLOCK TABLES')
                        snapshot.write_bytes(data)
                        report=json.loads(subprocess.run([str(probe),str(snapshot)],capture_output=True,text=True,check=True).stdout)
                        if not require_purged or report['DeleteMarked']==0:break
                        time.sleep(1)
                    else:raise RuntimeError('purge did not finish within 30 captures: '+table)
                    if report['Rows']!=len(expected):raise RuntimeError('current row count mismatch '+name)
                    if phase=='marked' and report['DeleteMarked']==0:raise RuntimeError('missing real delete marks '+name)
                    index=next(i for i in identities if i['type']&1)
                    layout=dict(columns=columns,**{k:index[k] for k in ('root_page','index_id','space_id')})
                    if keys is None:layout['clustered_key']=dict(name='PRIMARY',hidden_row_id=True)
                    elif table=='unique':layout['clustered_key']=dict(name='u',columns=keys)
                    elif len(keys)==1:layout['primary_key']=keys[0]
                    else:layout['primary_keys']=keys
                    save(name+'.ibd.gz',data);save(name+'.expected.json.gz',expected);save(name+'.indexes.json.gz',identities)
                    (out/(name+'.json')).write_text(json.dumps(layout,indent=2)+'\n')
                    (out/(name+'.sql')).write_text(ddl)
                    manifest['cases'].append(dict(name=name,table=table,phase=phase,rows=len(expected),unordered=keys is None,bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),physical=report,**{k:index[k] for k in ('root_page','index_id','space_id')}))
                    print(name,report,flush=True)
                capture('before')
                # Global read view retains old versions without target-table MDL.
                query('START TRANSACTION WITH CONSISTENT SNAPSHOT','holder')
                query('START TRANSACTION')
                for sql in updates:query(sql.format(t=table))
                query('COMMIT')
                capture('marked')
                query('COMMIT','holder')
                capture('purged',True)
                if table=='lesson':
                    query("INSERT INTO `lesson` VALUES (3,333,REPEAT('r',256)),(4,NULL,'reuse')")
                    capture('reused',True)
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
