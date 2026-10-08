#!/usr/bin/env python3
"""Capture stage-33 clustered queries and SQL oracles in a new isolated database."""
import argparse,gzip,hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
from datetime import datetime,timezone

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True)
a=p.parse_args();out=Path(a.out)
if out.exists():p.error('output directory must not exist')
out.mkdir(parents=True)
db='innodb_reader_query_'+uuid.uuid4().hex[:12];log=[]
client=subprocess.Popen([a.mysql,'--no-defaults','-uroot','--socket='+a.socket,'--default-character-set=utf8mb4','--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,env=os.environ.copy())
def q(sql):
 log.append(sql+';');mark='end'+uuid.uuid4().hex;client.stdin.write(sql+";\nSELECT '"+mark+"';\n");client.stdin.flush();rows=[]
 while True:
  line=client.stdout.readline()
  if not line:raise RuntimeError('mysql exited')
  line=line.rstrip('\n')
  if line==mark:return rows
  rows.append(line)
def save(name,value):
 data=value if isinstance(value,bytes) else (json.dumps(value,ensure_ascii=False,indent=2)+'\n').encode()
 (out/name).write_bytes(gzip.compress(data,mtime=0))
def literal(value):
 if isinstance(value,int):return str(value)
 return "CONVERT(CONVERT(X'"+value.encode().hex()+"' USING utf8mb4) USING latin1) COLLATE latin1_bin"
def predicate(keys,directions,values,lower,inclusive):
 branches=[]
 for i,value in enumerate(values):
  equal=['`'+keys[j]+'`='+literal(values[j]) for j in range(i)]
  op='>' if lower!=directions[i] else '<'
  branches.append('('+' AND '.join(equal+['`'+keys[i]+'`'+op+literal(value)])+')')
 if inclusive:branches.append('('+' AND '.join('`'+k+'`='+literal(v) for k,v in zip(keys,values))+')')
 return '('+' OR '.join(branches)+')'
def query_sql(table,columns,keys,directions,spec):
 conditions=[]
 if 'Prefix' in spec:conditions += ['`'+k+'`='+literal(v) for k,v in zip(keys,spec['Prefix'])]
 for which in ['Lower','Upper']:
  if which in spec:conditions.append(predicate(keys,directions,spec[which]['Key'],which=='Lower',spec[which]['Inclusive']))
 sql='SELECT JSON_ARRAY('+','.join('`'+c['name']+'`' for c in columns)+') FROM `'+table+'`'
 if conditions:sql+=' WHERE '+' AND '.join(conditions)
 sql+=' ORDER BY '+','.join('`'+k+'`'+(' DESC' if d!=spec.get('Reverse',False) else ' ASC') for k,d in zip(keys,directions))
 if spec.get('Limit'):sql+=' LIMIT '+str(spec['Limit'])
 return sql
manifest=dict(database=db,generated_at=datetime.now(timezone.utc).isoformat(),status='incomplete',cases=[])
try:
 env=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
 if env['version']!='8.0.45' or env['page_size']!=16384 or env['checksum']!='crc32':raise RuntimeError('unsupported instance')
 q("SET SESSION sql_mode='STRICT_TRANS_TABLES'");q("SET SESSION time_zone='+00:00'");q('CREATE DATABASE '+db+' CHARACTER SET utf8mb4');q('USE '+db)
 def capture(table,columns,keys,directions,specs,compact=False):
  q(f'FLUSH TABLES `{table}` FOR EXPORT')
  try:
   data=(Path(env['datadir'])/db/(table+'.ibd')).read_bytes()
   with tempfile.TemporaryDirectory(prefix='query-sdi-') as work:
    file=Path(work)/'table.ibd';file.write_bytes(data);official=subprocess.check_output([str(Path(a.mysql).parent/'ibd2sdi'),str(file)])
   indexes=[json.loads(x) for x in q("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID WHERE t.NAME='"+db+'/'+table+"' ORDER BY i.INDEX_ID")]
   idx=next(x for x in indexes if x['type']&1)
   full=[json.loads(x) for x in q(query_sql(table,columns,keys,directions,{}))]
   queries=[]
   for i,spec in enumerate(specs):
    sql=query_sql(table,columns,keys,directions,spec)
    queries.append(dict(name=str(i),query=spec,sql=sql,rows=[json.loads(x) for x in q(sql)]))
   ddl='\n'.join(q('SHOW CREATE TABLE '+table))+'\n'
  finally:q('UNLOCK TABLES')
  schema=dict(columns=columns,**{k:idx[k] for k in ['root_page','index_id','space_id']})
  schema['primary_key' if len(keys)==1 else 'primary_keys']=keys[0] if len(keys)==1 else keys
  if compact:schema['row_format']='COMPACT'
  save(table+'.ibd.gz',data);save(table+'.expected.json.gz',full);save(table+'.queries.json.gz',queries);save(table+'.sdi.json.gz',official);save(table+'.indexes.json.gz',indexes)
  (out/(table+'.json')).write_text(json.dumps(schema,indent=2)+'\n');(out/(table+'.sql')).write_text(ddl)
  manifest['cases'].append(dict(name=table,rows=len(full),queries=len(queries),sha256=hashlib.sha256(data).hexdigest(),bytes=len(data)))
  print(table,len(full),'rows',len(queries),'SQL queries',flush=True)
 def point(key):return dict(Lower=dict(Key=key,Inclusive=True),Upper=dict(Key=key,Inclusive=True))
 def ranges(lo,hi):
  return [dict(Lower=dict(Key=lo,Inclusive=lc),Upper=dict(Key=hi,Inclusive=uc),Reverse=rev,Limit=lim) for lc in [False,True] for uc in [False,True] for rev in [False,True] for lim in [0,7]]
 q('CREATE TABLE signed_rows(id BIGINT PRIMARY KEY,payload LONGTEXT NULL) ROW_FORMAT=DYNAMIC')
 for start in range(-900,900,100):q('INSERT INTO signed_rows VALUES '+','.join('('+str(i*3)+",REPEAT('p',1000))" for i in range(start,start+100)))
 q("INSERT INTO signed_rows VALUES(-9223372036854775808,NULL),(9223372036854775807,'edge')")
 q('DELETE FROM signed_rows WHERE id=0');q("UPDATE signed_rows SET payload=REPEAT('😀',6000) WHERE id=3")
 specs=[point([n]) for n in [-9223372036854775808,-2701,-2700,-1,0,1,3,2697,2700,9223372036854775807]]+ranges([-120],[120])
 specs += [dict(Lower=dict(Key=[2600],Inclusive=False)),dict(Upper=dict(Key=[-2600],Inclusive=True),Reverse=True,Limit=9),dict(Reverse=True,Limit=5),dict(Lower=dict(Key=[120],Inclusive=True),Upper=dict(Key=[-120],Inclusive=True))]
 capture('signed_rows',[dict(name='id',type='BIGINT'),dict(name='payload',type='LONGTEXT',nullable=True)],['id'],[False],specs)
 q('CREATE TABLE mixed_rows(tenant INT NOT NULL,code VARCHAR(16) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,seq BIGINT UNSIGNED NOT NULL,payload VARCHAR(1600),PRIMARY KEY(tenant ASC,code DESC,seq ASC)) ROW_FORMAT=COMPACT')
 codes=['','a','a ','a\x00','b','€']
 for tenant in range(4):
  for start in range(0,200,50):q('INSERT INTO mixed_rows VALUES '+','.join('('+str(tenant)+','+literal(codes[i%6])+','+str(i)+",REPEAT('界',400))" for i in range(start,start+50)))
 specs=ranges([1,'b',0],[2,'a',180])+[dict(Prefix=p,Reverse=rev,Limit=lim) for p in [[1],[1,'a'],[1,'a '],[1,'a\x00'],[9]] for rev in [False,True] for lim in [0,3]]
 specs += [point([1,'a',1]),point([1,'a ',1]),point([1,'€',5]),point([1,'a',999])]
 capture('mixed_rows',[dict(name='tenant',type='INT'),dict(name='code',type='VARCHAR',max_chars=16,charset='latin1',collation='latin1_bin',descending=True),dict(name='seq',type='BIGINT',unsigned=True),dict(name='payload',type='VARCHAR',max_chars=1600,nullable=True)],['tenant','code','seq'],[False,True,False],specs,True)
 q('CREATE TABLE unsigned_rows(id BIGINT UNSIGNED PRIMARY KEY,payload INT NULL) ROW_FORMAT=DYNAMIC')
 values=[0,1,9223372036854775807,9223372036854775808,18446744073709551615]
 q('INSERT INTO unsigned_rows VALUES '+','.join('('+str(x)+',NULL)' for x in values))
 specs=[point([x]) for x in values]+[point([2])]+ranges([9223372036854775807],[18446744073709551615])
 capture('unsigned_rows',[dict(name='id',type='BIGINT',unsigned=True),dict(name='payload',type='INT',nullable=True)],['id'],[False],specs)
 manifest['status']='captured'
finally:
 save('writer.sql.gz',('\n'.join(log)+'\n').encode());client.stdin.close();client.wait(timeout=10);(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
