#!/usr/bin/env python3
"""Capture native 8.0.45 INSTANT row versions in a new independent database."""
import argparse,base64,gzip,hashlib,json,os,subprocess,tempfile,time,uuid
from pathlib import Path
from datetime import datetime,timezone

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);a=p.parse_args();out=Path(a.out)
 if out.exists():p.error('output must not exist')
 out.mkdir(parents=True);db='innodb_reader_instant_'+uuid.uuid4().hex[:12];log=[]
 client=subprocess.Popen([a.mysql,'--no-defaults','-uroot','--socket='+a.socket,'--default-character-set=utf8mb4','--max-allowed-packet=128M','--batch','--raw','--skip-column-names','--unbuffered'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True,env=os.environ.copy())
 def q(sql):
  log.append(sql+';');mark='end'+uuid.uuid4().hex;client.stdin.write(sql+";\nSELECT '"+mark+"';\n");client.stdin.flush();rows=[]
  while True:
   line=client.stdout.readline()
   if not line:raise RuntimeError('mysql exited')
   line=line.rstrip('\n')
   if line==mark:return rows
   rows.append(line)
 def save(name,data):
  if not isinstance(data,bytes):data=(json.dumps(data,ensure_ascii=False,indent=2)+'\n').encode()
  (out/name).write_bytes(gzip.compress(data,mtime=0))
 def col(name,typ,**kw):return dict(name=name,type=typ,**kw)
 def props(s):return dict(x.split('=',1) for x in s.split(';') if x)
 manifest=dict(database=db,generated_at=datetime.now(timezone.utc).isoformat(),cases=[],status='incomplete')
 try:
  env=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
  if env['version']!='8.0.45' or env['page_size']!=16384 or not env['file_per_table']:raise RuntimeError('unsupported instance')
  q("SET SESSION sql_mode='STRICT_TRANS_TABLES'");q("SET SESSION time_zone='+00:00'");q('SET SESSION sql_require_primary_key=OFF');q('SET SESSION sql_generate_invisible_primary_key=OFF');q('CREATE DATABASE '+db+' CHARACTER SET utf8mb4');q('USE '+db)
  with tempfile.TemporaryDirectory(prefix='innodb-instant-') as tmp:
   tmp=Path(tmp);snapshot=tmp/'table.ibd';probe=tmp/'probe';subprocess.run(['go','build','-o',str(probe),'./examples/changes'],check=True,env=dict(os.environ,GOCACHE='/tmp/innodb-go-build-cache'))
   def capture(table,phase,definitions,keys=['id']):
    name=table+'_'+phase;q(f'FLUSH TABLES `{table}` FOR EXPORT')
    try:
     data=(Path(env['datadir'])/db/(table+'.ibd')).read_bytes();snapshot.write_bytes(data)
     raw=subprocess.check_output([str(Path(a.mysql).parent/'ibd2sdi'),str(snapshot)]);official=json.loads(raw);dd=next(x['object']['dd_object'] for x in official[1:] if x['object']['dd_object_type']=='Table')
     byname={c['name']:c for c in definitions};live=[c for c in dd['columns'] if c['hidden']==1];columns=[byname[c['name']] for c in live]
     expr=[]
     for c in columns:
      n='`'+c['name']+'`';t=c['type']
      if t in ('BINARY','VARBINARY') or t.endswith('BLOB'):n='HEX('+n+')'
      if t in ('DECIMAL','DATE','DATETIME','TIME','TIMESTAMP'):n='CAST('+n+' AS CHAR)'
      if t=='BIT':n='CAST('+n+' AS UNSIGNED)'
      expr.append(n)
     rows=q('SELECT JSON_ARRAY('+','.join(expr)+f') FROM `{table}`'+(' ORDER BY '+','.join('`'+k+'`' for k in keys) if keys else ''))
     indexes=[json.loads(x) for x in q("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{db}/{table}' ORDER BY i.INDEX_ID")];idx=next(i for i in indexes if i['type']&1)
     ddl='\n'.join(q(f'SHOW CREATE TABLE `{table}`'))+'\n'
    finally:q('UNLOCK TABLES')
    schema=dict(columns=columns,**{k:idx[k] for k in ('root_page','index_id','space_id')})
    if keys:schema['primary_key']=keys[0]
    else:schema['clustered_key']=dict(name='PRIMARY',hidden_row_id=True)
    fields=[];version=0
    # Manual type declarations, plus official SDI lifecycle/default bytes. This
    # is independent serialization, not a second source of physical metadata.
    for c in dd['columns']:
     cp=props(c['se_private_data'])
     if 'physical_pos' not in cp or c['name'] in ('DB_ROW_ID','DB_TRX_ID','DB_ROLL_PTR'):continue
     added=int(cp.get('version_added',0));dropped=int(cp.get('version_dropped',0));version=max(version,added,dropped)
     f=dict(position=int(cp['physical_pos']),column=-1 if dropped else next(i for i,x in enumerate(columns) if x['name']==c['name']))
     if added:f['added']=added
     if dropped:
      f['dropped']=dropped
      # MySQL renames dropped fields using version and physical position.
      oldname=c['name'].split('_',4)[-1] if c['name'].startswith('!hidden!_dropped_') else c['name']
      definition=dict(byname[oldname]);definition['name']=c['name'];f['dropped_column']=definition
     if 'default' in cp:
      rawdefault=bytes.fromhex(cp['default']);f['default']={} if not rawdefault else dict(data=base64.b64encode(rawdefault).decode())
     if 'default_null' in cp:f['default']=dict(null=True)
     fields.append(f)
    if fields:schema['instant']=dict(version=version,fields=sorted(fields,key=lambda f:f['position']))
    save(name+'.ibd.gz',data);save(name+'.sdi.json.gz',raw);save(name+'.expected.json.gz',('['+','.join(rows)+']\n').encode());save(name+'.indexes.json.gz',indexes)
    (out/(name+'.json')).write_text(json.dumps(schema,ensure_ascii=False,indent=2)+'\n');(out/(name+'.sql')).write_text(ddl)
    run=subprocess.run([str(probe),str(snapshot)],capture_output=True,text=True)
    if run.returncode:raise RuntimeError(name+': '+run.stderr)
    report=json.loads(run.stdout)
    if report['Rows']!=len(rows):raise RuntimeError('row mismatch')
    manifest['cases'].append(dict(name=name,table=table,phase=phase,rows=len(rows),unordered=not keys,sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),version=version,physical=report))
    print(name,report,'version',version,flush=True)
   defs=[col('id','INT'),col('a','VARCHAR',nullable=True,max_chars=30),col('b','INT'),col('payload','LONGTEXT',nullable=True)]
   q('CREATE TABLE lesson(id INT PRIMARY KEY,a VARCHAR(30),b INT NOT NULL,payload LONGTEXT) ROW_FORMAT=DYNAMIC');q("INSERT INTO lesson VALUES(1,'old',42,REPEAT('界😀',12000)),(2,NULL,43,NULL)");capture('lesson','before',defs)
   defs += [col('c','INT'),col('d','VARCHAR',nullable=True,max_chars=30),col('e','VARBINARY',max_bytes=20)]
   q("ALTER TABLE lesson ADD c INT NOT NULL DEFAULT 77 FIRST, ADD d VARCHAR(30) NULL DEFAULT NULL AFTER id, ADD e VARBINARY(20) NOT NULL DEFAULT X'00FF',ALGORITHM=INSTANT");q("INSERT INTO lesson(id,a,b) VALUES(3,'v1',44)");capture('lesson','add',defs)
   q('ALTER TABLE lesson ALTER c SET DEFAULT 99, ALGORITHM=INSTANT');q("INSERT INTO lesson(id,a,b) VALUES(4,'new default',45)");capture('lesson','default_changed',defs)
   q('ALTER TABLE lesson DROP a,DROP payload,DROP c,ALGORITHM=INSTANT');q('INSERT INTO lesson(id,b) VALUES(5,46)');capture('lesson','drop',defs)
   # Re-add the same SQL name with the same type; historical definition stays separate.
   q("ALTER TABLE lesson ADD a VARCHAR(30) NULL DEFAULT 'again' FIRST,ALGORITHM=INSTANT");q('INSERT INTO lesson(id,b) VALUES(6,47)');capture('lesson','readd',defs)
   q("UPDATE lesson SET d='changed' WHERE id=1");q('DELETE FROM lesson WHERE id=2');capture('lesson','updated',defs)
   q('ALTER TABLE lesson FORCE,ALGORITHM=INPLACE');capture('lesson','rebuilt',defs)
   defs=[col('id','INT'),col('padding','VARCHAR',max_chars=1600)]+[col('n'+str(i),'INT',nullable=True) for i in range(8)]
   q('CREATE TABLE tree(id INT PRIMARY KEY,padding VARCHAR(1600) NOT NULL,'+','.join('n'+str(i)+' INT NULL' for i in range(8))+') ROW_FORMAT=DYNAMIC')
   for start in range(0,800,100):q('INSERT INTO tree VALUES '+','.join('('+str(i)+",REPEAT('t',1200),"+','.join(['NULL']*8)+')' for i in range(start,start+100)))
   capture('tree','before',defs)
   defs += [col('m'+str(i),'VARCHAR',nullable=True,max_chars=10) for i in range(9)]
   q('ALTER TABLE tree '+','.join("ADD m"+str(i)+" VARCHAR(10) NULL DEFAULT 'x' FIRST" for i in range(9))+',ALGORITHM=INSTANT')
   for start in range(800,1000,100):q('INSERT INTO tree(id,padding) VALUES '+','.join('('+str(i)+",REPEAT('n',1200))" for i in range(start,start+100)))
   capture('tree','add',defs)
   q('ALTER TABLE tree '+','.join('DROP n'+str(i) for i in range(8))+',ALGORITHM=INSTANT');q("INSERT INTO tree(id,padding) VALUES(1000,REPEAT('z',1200))");capture('tree','drop',defs)
   q('ALTER TABLE tree FORCE,ALGORITHM=INPLACE');capture('tree','rebuilt',defs)
   defs=[col('v','VARCHAR',max_chars=40,nullable=True),col('n','INT',nullable=True),col('added','INT',nullable=True)]
   q('CREATE TABLE hidden(v VARCHAR(40),n INT) ROW_FORMAT=DYNAMIC');q("INSERT INTO hidden VALUES('same',1),('same',1),(NULL,2)")
   q('ALTER TABLE hidden ADD added INT NULL DEFAULT 88 FIRST,ALGORITHM=INSTANT');q("INSERT INTO hidden(v,n) VALUES('new',3)");capture('hidden','add',defs,None)
   q('ALTER TABLE hidden DROP n,ALGORITHM=INSTANT');q("INSERT INTO hidden(v) VALUES('v2')");capture('hidden','drop',defs,None)
   q('ALTER TABLE hidden FORCE,ALGORITHM=INPLACE');capture('hidden','rebuilt',defs,None)
   defs=[col('id','INT')];q('CREATE TABLE types(id INT PRIMARY KEY) ROW_FORMAT=DYNAMIC');q('INSERT INTO types VALUES(1)')
   additions=[('u',"BIGINT UNSIGNED NOT NULL DEFAULT 18446744073709551615",col('u','BIGINT',unsigned=True)),('d',"DECIMAL(20,3) NOT NULL DEFAULT -12345678901234567.890",col('d','DECIMAL',precision=20,scale=3)),('ch',"CHAR(8) NOT NULL DEFAULT '界😀'",col('ch','CHAR',max_chars=8)),('v',"VARCHAR(20) NOT NULL DEFAULT ''",col('v','VARCHAR',max_chars=20)),('bin',"BINARY(4) NOT NULL DEFAULT X'00FF'",col('bin','BINARY',max_bytes=4)),('dt',"DATETIME(6) NOT NULL DEFAULT '2024-02-29 12:34:56.123456'",col('dt','DATETIME',fsp=6)),('tm',"TIME(3) NOT NULL DEFAULT '-12:34:56.123'",col('tm','TIME',fsp=3)),('en',"ENUM('','a','界') NOT NULL DEFAULT '界'",col('en','ENUM',enum_values=['','a','界'])),('st',"SET('a','b','界') NOT NULL DEFAULT 'a,界'",col('st','SET',set_values=['a','b','界'])),('bits',"BIT(64) NOT NULL DEFAULT b'1111111111111111111111111111111111111111111111111111111111111111'",col('bits','BIT',bit_length=64)),('j','JSON NULL',col('j','JSON',nullable=True)),('blob','BLOB NULL',col('blob','BLOB',nullable=True))]
   defs += [x[2] for x in additions];q('ALTER TABLE types '+','.join('ADD `'+n+'` '+sql for n,sql,c in additions)+',ALGORITHM=INSTANT');q('INSERT INTO types(id) VALUES(2)');capture('types','defaults',defs)
   defs=[col('id','INT'),col('v','INT',nullable=True)];q('CREATE TABLE versions(id INT PRIMARY KEY) ROW_FORMAT=DYNAMIC');q('INSERT INTO versions VALUES(0)')
   for i in range(32):
    q('ALTER TABLE versions ADD v INT NULL DEFAULT '+str(i)+',ALGORITHM=INSTANT');q('INSERT INTO versions(id) VALUES('+str(i+1)+')');q('ALTER TABLE versions DROP v,ALGORITHM=INSTANT')
   q('INSERT INTO versions(id) VALUES(100)')
   capture('versions','v64',defs)
   q('ALTER TABLE versions FORCE,ALGORITHM=INPLACE');capture('versions','rebuilt',defs)
  manifest['status']='captured'
 finally:
  save('writer.sql.gz',('\n'.join(log)+'\n').encode());client.stdin.close();client.wait(timeout=10);(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
if __name__=='__main__':main()
