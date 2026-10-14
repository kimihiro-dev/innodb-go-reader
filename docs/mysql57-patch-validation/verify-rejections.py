import json,os,pathlib,subprocess,sys
repo=pathlib.Path('/Users/kimihiro/workspace/codespace/go/src/innodb-go-reader');sys.path.insert(0,str(repo/'scripts'))
from mysql57_schema import Client
work=pathlib.Path('/tmp/innodb-57-patch-run');report={}
for version in ['5.7.9','5.7.10','5.7.11','5.7.12','5.7.28','5.7.43']:
 c=json.loads((work/('private-'+version+'.json')).read_text());mysql=Client(c['container']);database='innodb_patch_reject';out=work/('reject-'+version);out.mkdir()
 try:
  statements=['CREATE DATABASE '+database+' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin','USE '+database,
   'CREATE TABLE redundant(id INT PRIMARY KEY,p INT) ROW_FORMAT=REDUNDANT',
   'CREATE TABLE compressed(id INT PRIMARY KEY,p INT) ROW_FORMAT=COMPRESSED KEY_BLOCK_SIZE=8',
   'CREATE TABLE fts(id INT PRIMARY KEY,p TEXT,FULLTEXT KEY ft(p)) ROW_FORMAT=DYNAMIC',
   'CREATE TABLE hidden_doc(id INT PRIMARY KEY,p TEXT,FULLTEXT KEY ft(p)) ROW_FORMAT=DYNAMIC',
   'ALTER TABLE hidden_doc DROP INDEX ft',
   'CREATE TABLE indexed_virtual(id INT PRIMARY KEY,p INT,v INT AS(p+1) VIRTUAL,KEY vk(v)) ROW_FORMAT=DYNAMIC',
   'CREATE TABLE charset(id INT PRIMARY KEY,p VARCHAR(20) CHARACTER SET ucs2) ROW_FORMAT=DYNAMIC',
   'CREATE TABLE collation(id INT PRIMARY KEY,p VARCHAR(20) COLLATE utf8mb4_general_ci,KEY pk(p)) ROW_FORMAT=DYNAMIC']
  for sql in statements:mysql.query(sql)
  report[version]=[]
  for name in ['redundant','compressed','fts','hidden_doc','indexed_virtual','charset','collation']:
   env=os.environ.copy();env.update(PATH='',INNODB_PATCH_PASSWORD=c['password']);dest=out/name
   p=subprocess.run(['/tmp/innodb-57-patch-reader','schema','--host',c['host'],'--port',str(c['port']),'--user','schema_reader','--password-env','INNODB_PATCH_PASSWORD','--database',database,'--table',name,'--indexes','--output',str(dest)],env=env,capture_output=True,text=True,timeout=30)
   assert p.returncode==1 and not dest.exists() and not p.stdout,(version,name,p.returncode,p.stderr)
   assert c['password'] not in p.stderr
   (out/(name+'.stderr')).write_text(p.stderr);report[version].append({'case':name,'exit_code':p.returncode,'diagnostic':json.loads(p.stderr)})
  assert not list(out.glob('.innodb-schema-*'))
 finally:mysql.close()
 print(version,'7 unsupported layouts rejected',flush=True)
(work/'rejections-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
