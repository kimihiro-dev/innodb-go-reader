#!/usr/bin/env python3
"""Capture paired 8.0.45 COMPACT/DYNAMIC snapshots in a new independent database."""
import argparse,base64,gzip,hashlib,json,os,subprocess,tempfile,uuid
from pathlib import Path
from datetime import datetime,timezone

def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql',required=True);p.add_argument('--socket',required=True);p.add_argument('--out',required=True);p.add_argument('--legacy',action='store_true',help='Official mysqld-debug session forces old BLOB writer; clearly separate from normal production path');a=p.parse_args();out=Path(a.out)
 if out.exists():p.error('output must not exist')
 out.mkdir(parents=True);db='innodb_reader_compact_'+uuid.uuid4().hex[:12];log=[]
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
 manifest=dict(writer='official-debug-noindex' if a.legacy else 'normal',database=db,generated_at=datetime.now(timezone.utc).isoformat(),cases=[],status='incomplete')
 try:
  env=json.loads(q("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0]);manifest['environment']=env
  if env['version']!=('8.0.45-debug' if a.legacy else '8.0.45') or env['page_size']!=16384 or not env['file_per_table']:raise RuntimeError('unsupported instance')
  q("SET SESSION sql_mode='STRICT_TRANS_TABLES'");q("SET SESSION time_zone='+00:00'");q('SET SESSION sql_require_primary_key=OFF');q('SET SESSION sql_generate_invisible_primary_key=OFF');q('CREATE DATABASE '+db+' CHARACTER SET utf8mb4');q('USE '+db)
  if a.legacy:q("SET SESSION debug='+d,lob_insert_noindex'")
  with tempfile.TemporaryDirectory(prefix='innodb-compact-') as tmp:
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
      if t in ('DECIMAL','DATE','DATETIME','TIME','TIMESTAMP','JSON'):n='CAST('+n+' AS CHAR)'
      if t=='BIT':n='CAST('+n+' AS UNSIGNED)'
      expr.append(n)
     rows=q('SELECT JSON_ARRAY('+','.join(expr)+f') FROM `{table}`'+(' ORDER BY '+','.join('`'+k+'`'+(' DESC' if byname[k].get('descending') else '') for k in keys) if keys else ''))
     indexes=[json.loads(x) for x in q("SELECT JSON_OBJECT('name',i.NAME,'root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE,'type',i.TYPE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{db}/{table}' ORDER BY i.INDEX_ID")];idx=next(i for i in indexes if i['type']&1)
     ddl='\n'.join(q(f'SHOW CREATE TABLE `{table}`'))+'\n'
    finally:q('UNLOCK TABLES')
    schema=dict(columns=columns,**{k:idx[k] for k in ('root_page','index_id','space_id')})
    if unique:schema['clustered_key']=dict(name=dd['indexes'][0]['name'],columns=keys)
    elif keys:
     if len(keys)==1:schema['primary_key']=keys[0]
     else:schema['primary_keys']=keys
    else:schema['clustered_key']=dict(name='PRIMARY',hidden_row_id=True)
    if dd['row_format']==5:schema['row_format']='COMPACT'
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
    manifest['cases'].append(dict(name=name,table=table,phase=phase,rows=len(rows),unordered=not keys,sha256=hashlib.sha256(data).hexdigest(),bytes=len(data),version=version,reject=reject,pair=table.rsplit('_',1)[0]+'_'+phase,format=table.rsplit('_',1)[1].upper()))
    print(name,'rows',len(rows),'virtual',len(virtual),'version',version,flush=True)
   for form in ('COMPACT','DYNAMIC'):
    tag=form.lower()
    defs=[col('id','INT'),col('txt','LONGTEXT',nullable=True),col('bin','LONGBLOB',nullable=True),col('v','VARCHAR',max_chars=10000,nullable=True),col('vb','VARBINARY',max_bytes=24000,nullable=True),col('j','JSON',nullable=True)]
    t='lesson_'+tag
    q(f'CREATE TABLE {t}(id INT PRIMARY KEY,txt LONGTEXT,bin LONGBLOB,v VARCHAR(10000),vb VARBINARY(24000),j JSON) ROW_FORMAT='+form)
    q(f"INSERT INTO {t} VALUES(1,REPEAT('😀界',10000),REPEAT(X'00FF0102',7000),REPEAT('界😀',3000),REPEAT(X'AA00FF',6000),JSON_OBJECT('text',REPEAT('界😀',5000),'n',18446744073709551615)),(2,NULL,NULL,NULL,NULL,NULL),(3,'',X'','',X'',JSON_OBJECT())");capture(t,'initial',defs)
    q(f"UPDATE {t} SET txt=REPEAT('更新😀',13000),bin=REPEAT(X'AB00',50000),v=REPEAT('😀',10000),vb=REPEAT(X'FF00AA',7000),j=JSON_SET(j,'$.text',REPEAT('更',16000)) WHERE id=1");capture(t,'grown',defs)
    if a.legacy:q("SET SESSION debug='-d,lob_insert_noindex'")
    q(f"UPDATE {t} SET txt=REPEAT('new',30000) WHERE id=1");capture(t,'mixed_formats',defs)
    if a.legacy:q("SET SESSION debug='+d,lob_insert_noindex'")
    q(f"UPDATE {t} SET txt='short',bin=X'00',v='',vb=X'',j=JSON_OBJECT('n',1) WHERE id=1");capture(t,'shrunk',defs)
    q(f'UPDATE {t} SET txt=NULL,bin=NULL,v=NULL,vb=NULL,j=NULL WHERE id=1');q(f'DELETE FROM {t} WHERE id=2');capture(t,'null_deleted',defs)
    defs=[col('id','INT'),col('payload','LONGBLOB',nullable=True)]
    t='bounds_'+tag;q(f'CREATE TABLE {t}(id INT PRIMARY KEY,payload LONGBLOB) ROW_FORMAT='+form)
    sizes=[0,1,767,768,769,7900,8000,8100,16000,17098,17099,33428,33429]
    for i,n in enumerate(sizes):q(f"INSERT INTO {t} VALUES({i},REPEAT(X'FF',{n}))")
    q(f'INSERT INTO {t} VALUES(99,NULL)');capture(t,'initial',defs)
    defs=[col('id','INT'),col('padding','VARCHAR',max_chars=1600),col('copy','VARCHAR',max_chars=1600,nullable=True),col('added','INT',nullable=True)]
    t='tree_'+tag;q(f'CREATE TABLE {t}(id INT PRIMARY KEY,padding VARCHAR(1600) NOT NULL,v INT AS(id+1) VIRTUAL,copy VARCHAR(1600) AS(REVERSE(padding)) STORED) ROW_FORMAT='+form)
    for start in range(0,600,100):q(f'INSERT INTO {t}(id,padding) VALUES '+','.join('('+str(i)+",REPEAT('t',1200))" for i in range(start,start+100)))
    capture(t,'initial',defs)
    q(f'ALTER TABLE {t} ADD added INT NULL DEFAULT 88 FIRST,ALGORITHM=INSTANT');q(f"INSERT INTO {t}(id,padding) VALUES(600,REPEAT('x',1200))");capture(t,'instant',defs)
    q(f'ALTER TABLE {t} FORCE,ALGORITHM=INPLACE');capture(t,'rebuilt',defs)
    defs=[col('a','INT',nullable=True),col('payload','LONGTEXT',nullable=True)]
    t='hidden_'+tag;q(f'CREATE TABLE {t}(a INT,payload LONGTEXT) ROW_FORMAT='+form);q(f"INSERT INTO {t} VALUES(4,REPEAT('😀界',10000)),(4,REPEAT('😀界',10000)),(NULL,NULL)");capture(t,'initial',defs,None)
    defs=[col('seq','INT'),col('k','VARBINARY',max_bytes=767,descending=True),col('txt','VARCHAR',max_chars=1000,nullable=True)]
    t='composite_'+tag;q(f'CREATE TABLE {t}(seq INT NOT NULL,k VARBINARY(767) NOT NULL,txt VARCHAR(1000),PRIMARY KEY(k DESC,seq)) ROW_FORMAT='+form)
    for start in range(0,100,25):q(f'INSERT INTO {t} VALUES '+','.join('('+str(i)+",CONCAT(REPEAT(X'FF',766),UNHEX(LPAD(HEX("+str(i%5)+"),2,'0'))),REPEAT('界',500))" for i in range(start,start+25)))
    capture(t,'initial',defs,['k','seq'])
  manifest['status']='captured'
 finally:
  save('writer.sql.gz',('\n'.join(log)+'\n').encode());client.stdin.close();client.wait(timeout=10);(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
if __name__=='__main__':main()
