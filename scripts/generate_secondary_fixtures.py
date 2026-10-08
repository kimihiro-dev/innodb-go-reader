#!/usr/bin/env python3
"""Capture stage-34 secondary indexes in a fresh database; credentials via MYSQL_PWD."""
import argparse,gzip,hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
from datetime import datetime,timezone
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);a=p.parse_args()
out=Path(a.out)
if out.exists():p.error('output must not exist')
out.mkdir(parents=True);db='innodb_reader_secondary_'+uuid.uuid4().hex[:12];logs={'writer':[],'holder':[]};clients={}
for who in logs:
 clients[who]=subprocess.Popen([a.mysql,'--no-defaults','-uroot','--socket='+a.socket,'--default-character-set=utf8mb4','--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,env=os.environ.copy())
def q(sql,who='writer'):
 logs[who].append(sql+';');c=clients[who];mark='end'+uuid.uuid4().hex;c.stdin.write(sql+";\nSELECT '"+mark+"';\n");c.stdin.flush();rows=[]
 while True:
  line=c.stdout.readline()
  if not line:raise RuntimeError('mysql exited')
  line=line.rstrip('\n')
  if line==mark:return rows
  rows.append(line)
def save(name,value):
 data=value if isinstance(value,bytes) else (json.dumps(value,ensure_ascii=False,indent=2)+'\n').encode();(out/name).write_bytes(gzip.compress(data,mtime=0))
def spec(name,expr,order):return dict(name=name,expressions=expr,order=order)
cases=[]
def add(name,ddl,rows,full,indexes,**kw):cases.append(dict(name=name,ddl=ddl,rows=rows,full=full,indexes=indexes,**kw))
add('tiny','id TINYINT PRIMARY KEY,n TINYINT NULL,KEY n_idx(n)',[f'({i},{"NULL" if i%3==0 else i%5})' for i in range(-90,91)],['id','n'],[spec('n_idx',['n','id'],'n,id')])
add('empty','id INT PRIMARY KEY,n INT NULL,KEY n_idx(n)',[],['id','n'],[spec('n_idx',['n','id'],'n,id')])
add('integer','id BIGINT PRIMARY KEY,n INT NULL,u BIGINT UNSIGNED NULL,KEY n_idx(n DESC),UNIQUE KEY u_idx(u)',[f'({i},{"NULL" if i%11==0 else i%17},{"NULL" if i%7==0 else 2**64-1-i})' for i in range(2500)],['id','n','u'],[spec('n_idx',['n','id'],'n DESC,id'),spec('u_idx',['u','id'],'u,id')])
add('deep','a VARCHAR(200) COLLATE utf8mb4_bin NOT NULL,id INT NOT NULL,b VARCHAR(200) COLLATE utf8mb4_bin NULL,n INT NULL,PRIMARY KEY(a DESC,id),KEY b_idx(b DESC,n)',[f"(CONCAT(REPEAT('中',150),LPAD({i//4},5,'0')),{i},IF({i}%19=0,NULL,CONCAT(REPEAT('文',150),LPAD({i//13},5,'0'))),IF({i}%7=0,NULL,{i}%5))" for i in range(2000)],['a','id','b','n'],[spec('b_idx',['b','n','a','id'],'b DESC,n,a DESC,id')])
add('overlap','code VARCHAR(100) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL,id INT NOT NULL,payload TEXT,PRIMARY KEY(code DESC,id),KEY prefix_idx(code(2)),KEY overlap_idx(id DESC,code)',[f"(CONCAT('ab',LPAD({i},5,'0')),{i},REPEAT('p',100))" for i in range(120)],['code','id','payload'],[spec('prefix_idx',['LEFT(code,2)','code','id'],'LEFT(code,2),code DESC,id'),spec('overlap_idx',['id','code'],'id DESC,code')])
add('wide_prefix','id INT PRIMARY KEY,v VARCHAR(400) CHARACTER SET latin1 COLLATE latin1_bin NULL,KEY v_idx(v(200))',[f"({i},CONCAT(REPEAT('x',{n-3}),LPAD({i},3,'0')))" for i,n in enumerate([3,127,128,199,200,201,300,400])]+['(9,NULL)',"(10,'')"],['id','v'],[spec('v_idx',['LEFT(v,200)','id'],'LEFT(v,200),id')])
add('compact','id INT PRIMARY KEY,c CHAR(40) COLLATE utf8mb4_bin NULL,l CHAR(40) CHARACTER SET latin1 COLLATE latin1_bin NULL,b VARBINARY(300) NULL,f BINARY(8) NOT NULL,KEY c_idx(c(4) DESC,l(4)),KEY b_idx(b(160),f(4))',[f"({i},IF({i}%5=0,NULL,CONCAT('中😀',LPAD({i},3,'0'))),'ab',REPEAT(X'00FF',100),X'0102030405060708')" for i in range(80)],['id','c','l','HEX(b)','HEX(f)'],[spec('c_idx',['LEFT(c,4)','LEFT(l,4)','id'],'LEFT(c,4) DESC,LEFT(l,4),id'),spec('b_idx',['HEX(LEFT(b,160))','HEX(LEFT(f,4))','id'],'LEFT(b,160),LEFT(f,4),id')],format='COMPACT')
add('rowid','n INT NULL,k VARCHAR(30) COLLATE utf8mb4_bin NULL,KEY n_idx(n),KEY k_idx(k DESC)',["(NULL,NULL)","(1,'same')","(1,'same')","(2,'中')","(1,'')"],['n','k'],[spec('n_idx',['n'],'n'),spec('k_idx',['k'],'k DESC')],rowid=True)
add('unique_cluster','a INT NOT NULL,n INT NULL,u INT NULL,UNIQUE KEY cluster_key(a),KEY n_idx(n),UNIQUE KEY u_idx(u)',[f'({i},{i%5},{"NULL" if i%4==0 else i})' for i in range(100)],['a','n','u'],[spec('n_idx',['n','a'],'n,a'),spec('u_idx',['u','a'],'u,a')])
add('generated','id INT PRIMARY KEY,n INT NULL,g INT GENERATED ALWAYS AS(n*2) STORED INVISIBLE,v INT GENERATED ALWAYS AS(n+1) VIRTUAL,KEY g_idx(g)',[f'({i},{i%9})' for i in range(160)],['id','n','g'],[spec('g_idx',['g','id'],'g,id')],insert='(id,n)',alter='ALTER TABLE `{t}` ADD COLUMN added INT DEFAULT 7, ALGORITHM=INSTANT',full_after=['id','n','g','added'])
add('scalars','id INT PRIMARY KEY,d DECIMAL(20,6) NULL,dt DATETIME(6) NULL,tm TIME(6) NULL,ts TIMESTAMP(6) NULL,b BIT(13) NULL,y YEAR NULL,da DATE NULL,KEY d_idx(d DESC),KEY temporal_idx(dt,tm DESC,ts),KEY bits_idx(b,y,da)',["(1,-1234567890.123456,'2020-01-01 01:02:03.123456','-00:00:00.000001','2020-01-01 00:00:00.000001',8191,2020,'2020-01-01')","(2,0.000000,'0000-00-00 00:00:00.000000','00:00:00.000000','0000-00-00 00:00:00.000000',0,0,'0000-00-00')","(3,NULL,NULL,NULL,NULL,NULL,NULL,NULL)"],['id','CAST(d AS CHAR)','CAST(dt AS CHAR)','CAST(tm AS CHAR)','CAST(ts AS CHAR)','CAST(b AS UNSIGNED)','y','CAST(da AS CHAR)'],[spec('d_idx',['CAST(d AS CHAR)','id'],'d DESC,id'),spec('temporal_idx',['CAST(dt AS CHAR)','CAST(tm AS CHAR)','CAST(ts AS CHAR)','id'],'dt,tm DESC,ts,id'),spec('bits_idx',['CAST(b AS UNSIGNED)','y','CAST(da AS CHAR)','id'],'b,y,da,id')])
add('changes','id INT PRIMARY KEY,n INT NULL,k VARCHAR(30) COLLATE utf8mb4_bin NOT NULL,KEY n_idx(n),KEY k_idx(k DESC)',[f"({i},{i%7},CONCAT('k',LPAD({i},3,'0')))" for i in range(100)],['id','n','k'],[spec('n_idx',['n','id'],'n,id'),spec('k_idx',['k','id'],'k DESC,id')],changes=True)
manifest=dict(database=db,generated_at=datetime.now(timezone.utc).isoformat(),status='incomplete',cases=[])
try:
 env=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
 assert env['version']=='8.0.45' and env['page_size']==16384 and env['file_per_table'] and env['checksum'] in ('crc32','strict_crc32')
 q("SET SESSION sql_mode='STRICT_TRANS_TABLES'");q("SET SESSION time_zone='+00:00'");q('SET SESSION sql_require_primary_key=OFF');q('SET SESSION sql_generate_invisible_primary_key=OFF')
 q(f'CREATE DATABASE `{db}` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin');q(f'USE `{db}`')
 for case in cases:
  name=case['name'];q(f'CREATE TABLE `{name}` ({case["ddl"]}) ENGINE=InnoDB ROW_FORMAT='+case.get('format','DYNAMIC'))
  for i in range(0,len(case['rows']),100):q(f'INSERT INTO `{name}`'+case.get('insert','')+' VALUES '+','.join(case['rows'][i:i+100]))
  if case.get('alter'):q(case['alter'].format(t=name))
  if case.get('changes'):
   q('START TRANSACTION WITH CONSISTENT SNAPSHOT','holder');q(f'UPDATE `{name}` SET n=100+n,k=CONCAT(k,"x") WHERE id<20');q(f'DELETE FROM `{name}` WHERE id>=90')
  q(f'FLUSH TABLES `{name}` FOR EXPORT')
  try:
   fullsql='SELECT JSON_ARRAY('+','.join(case.get('full_after',case['full']))+f') FROM `{name}`'
   full=[json.loads(x) for x in q(fullsql)]
   indexes=[]
   for index in case['indexes']:
    sql='SELECT JSON_ARRAY('+','.join(index['expressions'])+f') FROM `{name}` FORCE INDEX (`{index["name"]}`) ORDER BY '+index['order']
    indexes.append(dict(**index,sql=sql,rows=[json.loads(x) for x in q(sql)]))
   data=(Path(env['datadir'])/db/(name+'.ibd')).read_bytes()
   ddl='\n'.join(q(f'SHOW CREATE TABLE `{name}`'))
  finally:q('UNLOCK TABLES')
  save(name+'.ibd.gz',data);save(name+'.expected.json.gz',dict(sql=fullsql,rows=full));save(name+'.secondary.json.gz',indexes);(out/(name+'.sql')).write_text(ddl+'\n')
  with tempfile.TemporaryDirectory(prefix='secondary-sdi-') as temp:
   file=Path(temp)/'table.ibd';file.write_bytes(data);official=subprocess.check_output([str(Path(a.mysql).parent/'ibd2sdi'),str(file)]);save(name+'.sdi.json.gz',official)
  manifest['cases'].append(dict(name=name,rows=len(full),indexes=len(indexes),rowid=case.get('rowid',False),sha256=hashlib.sha256(data).hexdigest(),bytes=len(data)))
  if case.get('changes'):q('COMMIT','holder')
  print(name,len(full),len(indexes),flush=True)
 manifest['status']='captured'
finally:
 for who,c in clients.items():
  save(who+'.sql.gz',('\n'.join(logs[who])+'\n').encode());c.stdin.close();c.wait(timeout=20);c.stdout.close()
 (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
