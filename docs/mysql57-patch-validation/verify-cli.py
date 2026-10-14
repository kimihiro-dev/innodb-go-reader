import gzip,hashlib,json,os,pathlib,subprocess,sys,time
repo=pathlib.Path('/Users/kimihiro/workspace/codespace/go/src/innodb-go-reader');work=pathlib.Path('/tmp/innodb-57-patch-run');reader='/tmp/innodb-57-patch-reader'
report={'status':'incomplete','versions':{}}

def normalize(v):
 if isinstance(v,dict):
  v={k:normalize(x) for k,x in v.items()}
  if v.get('max_chars')==0:v.pop('max_chars')
  if v.get('nullable') is False:v.pop('nullable')
  return v
 if isinstance(v,list):return [normalize(x) for x in v]
 return v

def run(args,out,name,private,success=True):
 env=os.environ.copy();env.update(PATH='',INNODB_PATCH_PASSWORD=private['password'])
 p=subprocess.run([reader,*args],env=env,capture_output=True,text=True,timeout=60)
 assert private['password'] not in p.stdout+p.stderr
 (out/(name+'.stdout')).write_text(p.stdout);(out/(name+'.stderr')).write_text(p.stderr)
 assert (p.returncode==0)==success,(name,p.returncode,p.stderr[:1000])
 if p.stderr:json.loads(p.stderr)
 return p

for version in ['5.7.9','5.7.10','5.7.11','5.7.12','5.7.28','5.7.43']:
 private=json.loads((work/('private-'+version+'.json')).read_text());out=work/('cli-'+version);out.mkdir()
 base=['--host',private['host'],'--port',str(private['port']),'--user','schema_reader','--password-env','INNODB_PATCH_PASSWORD']
 current={'cases':[]};report['versions'][version]=current
 for kind in ['base','secondary']:
  fixture=work/(version+'-'+kind)
  for _ in range(120):
   if (fixture/'manifest.json').exists() and json.loads((fixture/'manifest.json').read_text())['status']=='captured':break
   time.sleep(1)
  manifest=json.loads((fixture/'manifest.json').read_text());assert manifest['status']=='captured'
  for case in manifest['cases']:
   name=kind+'-'+case['name'];reference=json.loads((fixture/(case['name']+'.json')).read_text());source=json.loads((fixture/(case['name']+'.source.json')).read_text());definition=out/name
   args=['schema',*base,'--database',source['database'],'--table',source['table'],'--output',str(definition)]
   if kind=='secondary':args+=['--indexes']
   run(args,out,name+'-schema',private)
   assert normalize(json.loads((definition/'schema.json').read_text()))==normalize(reference),(version,name,'schema')
   actual=json.loads((definition/'source.json').read_text())
   for key,value in source.items():assert actual[key]==value,(version,name,key)
   indexes=[]
   if kind=='secondary':
    indexes=json.loads((fixture/(case['name']+'.indexes.json')).read_text())
    assert normalize(json.loads((definition/'indexes.json').read_text()))==normalize(indexes)
   raw=gzip.decompress((fixture/(case['name']+'.ibd.gz')).read_bytes());assert hashlib.sha256(raw).hexdigest()==case['sha256'];physical=out/'table.ibd';physical.write_bytes(raw)
   flags=['--schema',str(definition/'schema.json')];indexflags=['--indexes',str(definition/'indexes.json')] if kind=='secondary' else []
   run(['metadata',*flags,*indexflags,str(physical)],out,name+'-metadata',private)
   p=run(['export',*flags,*(['--materialized'] if reference.get('virtual_columns') else []),str(physical)],out,name+'-export',private)
   records=[json.loads(line) for line in p.stdout.splitlines()];assert records[-1]['kind']=='end' and int(records[-1]['rows'])==case['rows']
   for index in indexes:run(['check','--scope','secondary',*flags,*indexflags,'--index',index['Name'],str(physical)],out,name+'-'+index['Name'],private)
   current['cases'].append({'kind':kind,'name':case['name'],'rows':case['rows'],'indexes':len(indexes),'models_and_raw_source_match':True})
  print(version,kind,'CLI verified',flush=True)
 physical.unlink()
 # TLS does not fall back, including for the early yaSSL builds.
 source=json.loads((work/(version+'-base')/'scalar_dynamic_initial.source.json').read_text())
 tlsdest=out/'tls';tlsexpected=int(version.split('.')[-1])>=28
 p=run(['schema',*base,'--tls','--tls-ca',private['ca'],'--database',source['database'],'--table',source['table'],'--output',str(tlsdest)],out,'tls',private,tlsexpected)
 assert tlsexpected or not tlsdest.exists()
 current['tls']={'expected_success':tlsexpected,'exit_code':p.returncode,'diagnostic':json.loads(p.stderr)}
 # Run the same Linux binary against the server's actual native Unix socket.
 env=os.environ.copy();env['INNODB_PATCH_PASSWORD']=private['password']
 subprocess.run(['docker','cp','/tmp/innodb-57-patch-reader-linux',private['container']+':/tmp/innodb-reader'],check=True,capture_output=True)
 socketdest='/tmp/innodb-unix-definitions'
 p=subprocess.run(['docker','exec','--env','INNODB_PATCH_PASSWORD','--env','PATH=',private['container'],'/tmp/innodb-reader','schema','--socket','/var/run/mysqld/mysqld.sock','--user','schema_reader','--password-env','INNODB_PATCH_PASSWORD','--database',source['database'],'--table',source['table'],'--output',socketdest],env=env,capture_output=True,text=True,timeout=60)
 assert p.returncode==0,(version,p.stderr);assert private['password'] not in p.stdout+p.stderr
 (out/'native-unix.stdout').write_text(p.stdout);(out/'native-unix.stderr').write_text(p.stderr)
 unix=out/'native-unix';subprocess.run(['docker','cp',private['container']+':'+socketdest,str(unix)],check=True,capture_output=True)
 assert normalize(json.loads((unix/'schema.json').read_text()))==normalize(json.loads((work/(version+'-base')/'scalar_dynamic_initial.json').read_text()))
 current['native_unix_socket']='passed';current['runtime_PATH']='empty';current['status']='complete'
 (work/'cli-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
 print(version,'TCP/native Unix/TLS capability verified',flush=True)
report['status']='complete';(work/'cli-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
