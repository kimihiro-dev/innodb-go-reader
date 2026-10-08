#!/usr/bin/env python3
"""Capture stage-35 projection queries with independent SQL predicates in a fresh database."""
import argparse,gzip,hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
from datetime import datetime,timezone
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);a=p.parse_args();out=Path(a.out)
if out.exists():p.error('output must not exist')
out.mkdir(parents=True);db='innodb_reader_secondary_query_'+uuid.uuid4().hex[:12];log=[]
client=subprocess.Popen([a.mysql,'--no-defaults','-uroot','--socket='+a.socket,'--default-character-set=utf8mb4','--max-allowed-packet=128M','--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,env=os.environ.copy())
def q(sql):
 log.append(sql+';');marker='end'+uuid.uuid4().hex;client.stdin.write(sql+";\nSELECT '"+marker+"';\n");client.stdin.flush();rows=[]
 while True:
  line=client.stdout.readline()
  if not line:raise RuntimeError('mysql exited')
  line=line.rstrip('\n')
  if line==marker:return rows
  rows.append(line)
def save(name,obj):
 b=obj if isinstance(obj,bytes) else (json.dumps(obj,ensure_ascii=False,indent=2)+'\n').encode();(out/name).write_bytes(gzip.compress(b,mtime=0))
def literal(x):
 if x is None:return 'NULL'
 if isinstance(x,int):return str(x)
 return "CONVERT(X'"+x.encode().hex()+"' USING utf8mb4) COLLATE utf8mb4_bin"
def equality(col,x):return '('+col+' <=> '+literal(x)+')'
def cmp(col,x,greater):
 if x is None:return '('+col+' IS NOT NULL)' if greater else 'FALSE'
 return '('+col+' > '+literal(x)+')' if greater else '('+col+' IS NULL OR '+col+' < '+literal(x)+')'
def bound(cols,desc,values,lower,inclusive):
 parts=[]
 for i,x in enumerate(values):parts.append('('+' AND '.join([equality(cols[j],values[j]) for j in range(i)]+[cmp(cols[i],x,lower!=desc[i])])+')')
 if inclusive:parts.append('('+' AND '.join(equality(c,v) for c,v in zip(cols,values))+')')
 return '('+' OR '.join(parts)+')'
def predicate(cols,desc,r):
 if 'Prefix' in r:return ' AND '.join(equality(c,v) for c,v in zip(cols,r['Prefix']))
 parts=[]
 for name,lower in [('Lower',True),('Upper',False)]:
  if name in r:parts.append(bound(cols,desc,r[name]['Key'],lower,r[name].get('Inclusive',False)))
 return ' AND '.join(parts) or 'TRUE'
def point(key):return dict(Lower=dict(Key=key,Inclusive=True),Upper=dict(Key=key,Inclusive=True))
manifest=dict(database=db,generated_at=datetime.now(timezone.utc).isoformat(),status='incomplete',cases=[])
try:
 env=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
 assert env['version']=='8.0.45' and env['page_size']==16384 and env['checksum'] in ('crc32','strict_crc32')
 q("SET SESSION sql_mode='STRICT_TRANS_TABLES'");q("SET SESSION time_zone='+00:00'");q(f'CREATE DATABASE `{db}` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin');q(f'USE `{db}`')
 q('CREATE TABLE prefixes(id INT PRIMARY KEY,name VARCHAR(64) NULL,rank_value INT NULL,marker INT NOT NULL,payload TEXT,KEY s(name(3),rank_value DESC)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
 names=[];bases=['abc','abd','a','','中文😀','ab\0','€xy','zzz']
 for i in range(480):names.append(None if i%29==0 else '' if i%31==0 else bases[i%8]+f'{i%40:03}')
 for start in range(0,480,80):q('INSERT INTO prefixes VALUES '+','.join(f"({i},{literal(names[i])},{'NULL' if i%11==0 else i%9},{i*3},REPEAT('p',{24000 if i%47==0 else 32}))" for i in range(start,start+80)))
 q('CREATE TABLE covering(id BIGINT PRIMARY KEY,n INT NULL,marker INT NOT NULL,payload VARCHAR(100),KEY s(n)) ENGINE=InnoDB ROW_FORMAT=COMPACT')
 for start in range(0,3000,100):q('INSERT INTO covering VALUES '+','.join(f"({2**53+i},{'NULL' if i%23==0 else i//3},{i},CONCAT('row',{i}))" for i in range(start,start+100)))
 q('CREATE TABLE huge(id INT PRIMARY KEY,n INT NOT NULL,marker INT NOT NULL,payload LONGBLOB,KEY s(n)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
 q("INSERT INTO huge VALUES (1,1,17,REPEAT(X'61',16777217)),(2,2,18,X'6162')")
 cases=[('prefixes',['name','rank_value'],[False,True],['LEFT(name,3)','rank_value','id'],[False,True,False],['id','name','rank_value','marker','payload'],480),('covering',['n'],[False],['n','id'],[False,False],['id','n','marker','payload'],3000),('huge',['n'],[False],['n','id'],[False,False],['id','n','marker','HEX(payload)'],2)]
 for name,cols,desc,order,order_desc,fullcols,count in cases:
  ranges=[{},dict(Reverse=True,Limit=3)]
  if name=='prefixes':
   for i in [0,1,7,11,31,40,47,75,160,479]:ranges.extend([point([names[i],None if i%11==0 else i%9]),dict(Prefix=[names[i]])])
   for low,high in [(['abc001',8],['abc039',0]),(['abc000',0],['abd039',8]),([None,None],['',0]),(['a001',7],['zzz039',1]),(['中文😀001',8],['中文😀039',0])]:
    for lc,uc in [(True,True),(False,False),(False,True),(True,False)]:ranges.append(dict(Lower=dict(Key=low,Inclusive=lc),Upper=dict(Key=high,Inclusive=uc)))
   ranges.extend([point(['abc-not-present',5]),dict(Lower=dict(Key=['zzz',1],Inclusive=True)),dict(Upper=dict(Key=['abc',1],Inclusive=True))])
   projections=[['id'],['name','rank_value','marker'],['payload','id']]
  elif name=='covering':
   for key in [None,0,1,499,999,1000,-1]:ranges.append(point([key]))
   for low,high in [(0,3),(499,501),(999,1000),(5,3),(None,0)]:
    for inclusive in [True,False]:ranges.append(dict(Lower=dict(Key=[low],Inclusive=inclusive),Upper=dict(Key=[high],Inclusive=inclusive)))
   projections=[['n','id'],['payload','marker','id']]
  else:ranges=[point([1]),point([2]),dict(Upper=dict(Key=[1],Inclusive=False))];projections=[['n','id'],['marker']]
  queries=[]
  for i,r in enumerate(ranges):
   for reverse in [False,True]:
    for projection in projections:
     actual=dict(r,Reverse=reverse)
     if i%4==1:actual['Limit']=2
     ordering=','.join(c+(' DESC' if d!=reverse else ' ASC') for c,d in zip(order,order_desc))
     sql='SELECT JSON_ARRAY('+','.join(projection)+f') FROM `{name}` FORCE INDEX(s) WHERE '+predicate(cols,desc,actual)+' ORDER BY '+ordering
     if actual.get('Limit'):sql+=' LIMIT '+str(actual['Limit'])
     queries.append(dict(query=dict(Range=actual,Columns=projection),sql=sql,rows=[json.loads(x) for x in q(sql)]))
  q(f'FLUSH TABLES `{name}` FOR EXPORT')
  try:
   data=(Path(env['datadir'])/db/(name+'.ibd')).read_bytes();ddl='\n'.join(q(f'SHOW CREATE TABLE `{name}`'))
   fullsql='SELECT JSON_ARRAY('+','.join(fullcols)+f') FROM `{name}` ORDER BY id'
   if name!='huge':save(name+'.expected.json.gz',dict(sql=fullsql,rows=[json.loads(x) for x in q(fullsql)]))
  finally:q('UNLOCK TABLES')
  save(name+'.ibd.gz',data);save(name+'.queries.json.gz',queries);(out/(name+'.sql')).write_text(ddl+'\n')
  with tempfile.TemporaryDirectory(prefix='secondary-query-sdi-') as temp:
   f=Path(temp)/'snapshot.ibd';f.write_bytes(data);save(name+'.sdi.json.gz',subprocess.check_output([str(Path(a.mysql).parent/'ibd2sdi'),str(f)]))
  manifest['cases'].append(dict(name=name,rows=count,queries=len(queries),sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),full_read_rejected=name=='huge'))
  print(name,count,len(queries),flush=True)
 manifest['status']='captured'
finally:
 save('writer.sql.gz',('\n'.join(log)+'\n').encode());(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');client.stdin.close();client.wait(timeout=20);client.stdout.close()
