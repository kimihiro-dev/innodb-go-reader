#!/usr/bin/env python3
"""Capture 8.0.45 generated/invisible columns in a new independent database."""
import argparse,base64,gzip,hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
from datetime import datetime,timezone

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);a=p.parse_args();out=Path(a.out)
 if out.exists():p.error('output must not exist')
 out.mkdir(parents=True);db='innodb_reader_generated_'+uuid.uuid4().hex[:12];log=[]
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
  with tempfile.TemporaryDirectory(prefix='innodb-generated-') as tmp:
   tmp=Path(tmp);snapshot=tmp/'table.ibd'
   def capture(table,phase,definitions,keys=['id'],reject=False,unique=False):
    name=table+'_'+phase;q(f'FLUSH TABLES `{table}` FOR EXPORT')
    try:
     data=(Path(env['datadir'])/db/(table+'.ibd')).read_bytes();snapshot.write_bytes(data)
     raw=subprocess.check_output([str(Path(a.mysql).parent/'ibd2sdi'),str(snapshot)]);official=json.loads(raw);dd=next(x['object']['dd_object'] for x in official[1:] if x['object']['dd_object_type']=='Table')
     byname={c['name']:c for c in definitions};live=[c for c in dd['columns'] if c['hidden'] in (1,4) and not c['is_virtual']];columns=[dict(byname[c['name']]) for c in live]
     for definition,c in zip(columns,live):
      if c['hidden']==4:definition['invisible']=True
      if c['generation_expression']:definition['generation_expression']=c['generation_expression']
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
    if unique:schema['clustered_key']=dict(name=dd['indexes'][0]['name'],columns=keys)
    elif keys:schema['primary_key']=keys[0]
    else:schema['clustered_key']=dict(name='PRIMARY',hidden_row_id=True)
    virtual=[dict(name=c['name'],ordinal=i+1,expression=c['generation_expression'],**({'invisible':True} if c['hidden']==4 else {})) for i,c in enumerate(c for c in dd['columns'] if c['hidden'] in (1,4)) if c['is_virtual']]
    if virtual:schema['virtual_columns']=virtual
    fields=[];version=0
    # Manual type declarations, plus official SDI lifecycle/default bytes. This
    # is independent serialization, not a second source of physical metadata.
    for c in dd['columns']:
     cp=props(c['se_private_data'])
     if c['is_virtual'] or 'physical_pos' not in cp or c['name'] in ('DB_ROW_ID','DB_TRX_ID','DB_ROLL_PTR'):continue
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
    manifest['cases'].append(dict(name=name,table=table,phase=phase,rows=len(rows),unordered=not keys,sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),version=version,reject=reject))
    print(name,'rows',len(rows),'virtual',len(virtual),'version',version,flush=True)
   defs=[col('id','INT'),col('a','INT',nullable=True),col('text','VARCHAR',max_chars=40,nullable=True),col('twice','INT',nullable=True),col('label','VARCHAR',max_chars=90,nullable=True),col('secret','VARBINARY',max_bytes=12,nullable=True)]
   q("CREATE TABLE stored_values(id INT PRIMARY KEY,a INT,text VARCHAR(40),twice INT AS(a*2) STORED,label VARCHAR(90) AS(CONCAT(text,':',a)) STORED,secret VARBINARY(12) INVISIBLE) ROW_FORMAT=DYNAMIC")
   q("INSERT INTO stored_values(id,a,text,secret) VALUES(1,7,'界😀',X'00FF'),(2,NULL,NULL,NULL),(3,-5,'',X'')");capture('stored_values','initial',defs)
   q("UPDATE stored_values SET a=8,text='new' WHERE id=1");q('ALTER TABLE stored_values ALTER COLUMN secret SET VISIBLE');capture('stored_values','updated_visible',defs)
   defs=[col('id','BIGINT',unsigned=True),col('a','INT',nullable=True),col('txt','LONGTEXT',nullable=True),col('stored_n','INT',nullable=True),col('stored_text','LONGTEXT',nullable=True),col('secret','VARBINARY',max_bytes=20,nullable=True),col('added','INT'),col('tail','VARCHAR',max_chars=30,nullable=True)]
   q("CREATE TABLE mixed(id BIGINT UNSIGNED INVISIBLE PRIMARY KEY,a INT,virt INT AS(a+1) VIRTUAL,txt LONGTEXT,stored_n INT AS(a*2) STORED INVISIBLE,stored_text LONGTEXT AS(CONCAT(txt,txt)) STORED,secret VARBINARY(20) INVISIBLE,vt VARCHAR(30) COLLATE utf8mb4_unicode_ci AS(LEFT(txt,3)) VIRTUAL INVISIBLE) ROW_FORMAT=DYNAMIC")
   q("INSERT INTO mixed(id,a,txt,secret) VALUES(1,7,REPEAT('界😀',6000),X'00FF'),(2,NULL,NULL,NULL),(3,-9,'',X'')");capture('mixed','initial',defs)
   q('ALTER TABLE mixed ADD added INT NOT NULL DEFAULT 77 FIRST, ADD tail VARCHAR(30) DEFAULT NULL, ALGORITHM=INSTANT');q("INSERT INTO mixed(id,a,txt,secret) VALUES(4,11,'short',X'0102')");capture('mixed','instant',defs)
   q('ALTER TABLE mixed DROP secret,ALGORITHM=INSTANT');q("UPDATE mixed SET a=-100,txt=REPEAT('更新',8000) WHERE id=1");q('DELETE FROM mixed WHERE id=3');capture('mixed','changed',defs)
   q('ALTER TABLE mixed FORCE,ALGORITHM=INPLACE');capture('mixed','rebuilt',defs)
   q('ALTER TABLE mixed DROP virt,DROP vt,ALGORITHM=INPLACE');capture('mixed','no_virtual',defs)
   defs=[col('id','INT'),col('padding','VARCHAR',max_chars=1600),col('copy','VARCHAR',max_chars=1600,nullable=True)]+[col('n'+str(i),'INT',nullable=True) for i in range(8)]+[col('added','INT',nullable=True)]
   q('CREATE TABLE tree(id INT PRIMARY KEY,padding VARCHAR(1600) NOT NULL,'+','.join('v'+str(i)+' INT AS(id+'+str(i)+') VIRTUAL' for i in range(9))+',copy VARCHAR(1600) AS(REVERSE(padding)) STORED,'+','.join('n'+str(i)+' INT NULL' for i in range(8))+') ROW_FORMAT=DYNAMIC')
   for start in range(0,600,100):q('INSERT INTO tree(id,padding) VALUES '+','.join('('+str(i)+",REPEAT('t',1200))" for i in range(start,start+100)))
   capture('tree','initial',defs)
   q('ALTER TABLE tree ADD added INT NULL DEFAULT 88 FIRST,ALGORITHM=INSTANT');q("INSERT INTO tree(id,padding) VALUES(600,REPEAT('x',1200))");capture('tree','instant',defs)
   defs=[col('a','INT',nullable=True),col('stored_n','INT',nullable=True),col('secret','VARCHAR',max_chars=20,nullable=True)]
   q('CREATE TABLE hidden(a INT,v INT AS(a+1) VIRTUAL,stored_n INT AS(a*2) STORED,secret VARCHAR(20) INVISIBLE) ROW_FORMAT=DYNAMIC');q("INSERT INTO hidden(a,secret) VALUES(4,'same'),(4,'same'),(NULL,NULL)");capture('hidden','initial',defs,None)
   defs=[col('a','INT'),col('stored_n','INT')]
   q('CREATE TABLE unique_stored(a INT NOT NULL,v INT AS(a-1) VIRTUAL,stored_n INT AS(a+1) STORED NOT NULL,UNIQUE KEY actual(stored_n)) ROW_FORMAT=DYNAMIC');q('INSERT INTO unique_stored(a) VALUES(9),(-2),(20)');capture('unique_stored','initial',defs,['stored_n'],unique=True)
   defs=[col('id','INT'),col('a','INT',nullable=True)]
   q('CREATE TABLE empty_values(id INT PRIMARY KEY,a INT,v INT AS(a+1) VIRTUAL) ROW_FORMAT=DYNAMIC');capture('empty_values','initial',defs)
   q('CREATE TABLE indexed(id INT PRIMARY KEY,a INT,v INT AS(a+1) VIRTUAL,KEY iv(v)) ROW_FORMAT=DYNAMIC');q('INSERT INTO indexed(id,a) VALUES(1,7),(2,NULL),(3,-9)');capture('indexed','initial',defs)
   q('CREATE TABLE functional(id INT PRIMARY KEY,a INT,KEY fx((a+1))) ROW_FORMAT=DYNAMIC');q('INSERT INTO functional VALUES(1,7)');capture('functional','rejected',defs,reject=True)
  manifest['status']='captured'
 finally:
  save('writer.sql.gz',('\n'.join(log)+'\n').encode());client.stdin.close();client.wait(timeout=10);(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
if __name__=='__main__':main()
