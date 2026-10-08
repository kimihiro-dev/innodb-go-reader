#!/usr/bin/env python3
"""Capture allocation-only snapshots in a fresh database; password is prompted, never saved."""
import argparse,getpass,gzip,hashlib,json,os,subprocess,time,uuid
from pathlib import Path
from datetime import datetime,timezone
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);a=p.parse_args();out=Path(a.out)
if out.exists():p.error('output must not exist')
env=os.environ.copy();env['MYSQL_PWD']=getpass.getpass('MySQL password: ')
client=subprocess.Popen([a.mysql,'--no-defaults','-uroot','--socket='+a.socket,'--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,env=env)
del env['MYSQL_PWD'];out.mkdir(parents=True);log=[];db='innodb_reader_space_'+uuid.uuid4().hex[:12]
def q(sql):
 log.append(sql+';');token='end'+uuid.uuid4().hex;client.stdin.write(sql+";\nSELECT '"+token+"';\n");client.stdin.flush();rows=[]
 while True:
  line=client.stdout.readline()
  if not line:raise RuntimeError('mysql exited')
  line=line.rstrip('\n')
  if line==token:return rows
  rows.append(line)
def save(name,obj):
 raw=obj if isinstance(obj,bytes) else (json.dumps(obj,indent=2)+'\n').encode();(out/name).write_bytes(gzip.compress(raw,compresslevel=6,mtime=0))
manifest={'database':db,'generated_at':datetime.now(timezone.utc).isoformat(),'status':'incomplete','cases':[]}
try:
 info=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=info
 assert info['version']=='8.0.45' and info['page_size']==16384 and info['checksum'] in ('crc32','strict_crc32') and info['file_per_table']==1
 q(f'CREATE DATABASE `{db}`');q(f'USE `{db}`');q('CREATE TABLE allocation_rows(id INT PRIMARY KEY,n INT NOT NULL,payload LONGBLOB,KEY s(n)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
 def capture(name):
  count=int(q('SELECT COUNT(*) FROM allocation_rows')[0]);q('FLUSH TABLES allocation_rows FOR EXPORT')
  try:
   raw=(Path(info['datadir'])/db/'allocation_rows.ibd').read_bytes();save(name+'.space.gz',raw)
   table=q("SELECT JSON_OBJECT('space',SPACE,'name',NAME,'row_format',ROW_FORMAT,'zip_page_size',ZIP_PAGE_SIZE,'space_type',SPACE_TYPE) FROM information_schema.INNODB_TABLES WHERE NAME='"+db+"/allocation_rows'")
   indexes=q("SELECT JSON_OBJECT('id',i.INDEX_ID,'name',i.NAME,'page',i.PAGE_NO,'space',i.SPACE) FROM information_schema.INNODB_INDEXES i JOIN information_schema.INNODB_TABLES t ON i.TABLE_ID=t.TABLE_ID WHERE t.NAME='"+db+"/allocation_rows' ORDER BY i.INDEX_ID")
   save(name+'.sql-metadata.json.gz',{'table':[json.loads(x) for x in table],'indexes':[json.loads(x) for x in indexes],'rows':count})
  finally:q('UNLOCK TABLES')
  manifest['cases'].append({'name':name,'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'rows':count});(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(name,len(raw)//16384,'pages',flush=True)
 capture('empty')
 for start in range(0,17000,200):q('INSERT INTO allocation_rows VALUES '+','.join(f"({i},{i%101},REPEAT(X'61',9000))" for i in range(start,start+200)))
 capture('grown')
 q('DELETE FROM allocation_rows WHERE id >= 10');time.sleep(2);capture('deleted')
 q('OPTIMIZE TABLE allocation_rows');capture('rebuilt')
 manifest['status']='captured'
finally:
 save('writer.sql.gz','\n'.join(log).encode());(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');client.stdin.close();client.wait(timeout=30)
