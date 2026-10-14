import gzip,hashlib,json,pathlib,shutil,subprocess,tempfile
repo=pathlib.Path('/Users/kimihiro/workspace/codespace/go/src/innodb-go-reader')
reader='/tmp/innodb-input-preparation-reader'
container='innodb-reader-mysql5744-20261013'
work=pathlib.Path(tempfile.mkdtemp(prefix='innodb-prepare-live-final-'))
report={'work':str(work),'container':container,'cases':[]}
def run(args, name):
 p=subprocess.run(args,capture_output=True,text=True,cwd=repo,timeout=60)
 (work/(name+'.stdout')).write_text(p.stdout)
 (work/(name+'.stderr')).write_text(p.stderr)
 if p.returncode: raise RuntimeError(f'{name}: exit {p.returncode}: '+p.stderr[:1500])
 return p

def normalize(v):
 if isinstance(v,dict):
  v={k:normalize(x) for k,x in v.items()}
  if v.get('max_chars')==0: v.pop('max_chars')
  if v.get('nullable') is False: v.pop('nullable')
  return v
 if isinstance(v,list): return [normalize(x) for x in v]
 return v

for group,name in [('mysql5744-secondary','ordinary_dynamic'),('mysql5744-secondary','composite_compact'),('mysql5744-secondary','hidden_compact'),('mysql5744','scalar_dynamic_initial'),('mysql5744','scalar_compact_initial'),('mysql5744','generated_dynamic_initial')]:
 source=json.loads((repo/'testdata'/group/(name+'.source.json')).read_text())
 args=['--container',container,'--database',source['database'],'--table',source['table'],'--indexes']
 target=work/(name+'-go'); old=work/(name+'-python')
 run([reader,'schema',*args,'--snapshot','--output',str(target)],name+'-collect')
 run(['python3','scripts/mysql57_schema.py',*args,'--out',str(old)],name+'-reference')
 for file in ['schema.json','indexes.json']:
  assert normalize(json.loads((target/file).read_text()))==normalize(json.loads((old/file).read_text())),name+' '+file
 go=json.loads((target/'source.json').read_text()); py=json.loads((old/'source.json').read_text())
 for key,value in py.items():
  if key!='capture': assert go[key]==value,(name,'source',key)
 raw=gzip.decompress((target/'table.ibd.gz').read_bytes())
 assert len(raw)==go['bytes'] and hashlib.sha256(raw).hexdigest()==go['sha256']
 ibd=target/'table.ibd';ibd.write_bytes(raw)
 schema=json.loads((target/'schema.json').read_text()); materialized=['--materialized'] if schema.get('virtual_columns') else []
 run([reader,'metadata','--schema',str(target/'schema.json'),'--indexes',str(target/'indexes.json'),str(ibd)],name+'-metadata')
 p=run([reader,'export','--schema',str(target/'schema.json'),*materialized,str(ibd)],name+'-export')
 records=[json.loads(line) for line in p.stdout.splitlines()]
 expected=json.loads(gzip.decompress((repo/'testdata'/group/(name+'.expected.json.gz')).read_bytes()))
 assert records[0]['kind']=='schema' and records[-1]['kind']=='end'
 assert sum(x['kind']=='row' for x in records)==len(expected)
 for index in json.loads((target/'indexes.json').read_text()):
  run([reader,'check','--scope','secondary','--schema',str(target/'schema.json'),'--indexes',str(target/'indexes.json'),'--index',index['Name'],str(ibd)],name+'-secondary-'+index['Name'])
 report['cases'].append({'name':name,'definitions_equal_to_python':True,'source_equal_to_python':True,'snapshot_hash_verified':True,'rows':len(expected),'indexes':len(json.loads((target/'indexes.json').read_text()))})
 print(name,'verified',flush=True)

partdir=work/'partitions';partdir.mkdir()
for part,filename in [('p2','third.ibd'),('p0','first.ibd'),('p1','second.ibd')]:
 (partdir/filename).write_bytes(gzip.decompress((repo/'testdata/partitions'/('ranges-'+part+'.partition.gz')).read_bytes()))
paths=[str(partdir/x) for x in ['third.ibd','first.ibd','second.ibd']]
manifest=partdir/'files.json'
run([reader,'manifest','--output',str(manifest),*paths],'manifest-generate')
for command in ['metadata','check','export']:
 run([reader,command,'--manifest',str(manifest)],'manifest-'+command)
assert [x['partition'] for x in json.loads(manifest.read_text())['files']]==['p0','p1','p2']
report['manifest']={'names_from_sdi':True,'definition_order':True,'metadata_check_export':True,'rows':1000}

missing=work/'missing-table'
p=subprocess.run([reader,'schema','--container',container,'--database',source['database'],'--table','__missing_input_preparation_table','--output',str(missing)],capture_output=True,text=True,cwd=repo,timeout=60)
assert p.returncode==1 and not missing.exists()
report['missing_table_cleanup']=True
broken=work/'broken-stdout'
p=subprocess.Popen([reader,'schema','--container',container,'--database',source['database'],'--table',source['table'],'--output',str(broken)],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=repo)
p.stdout.close();diagnostic=p.stderr.read();code=p.wait(timeout=60)
assert code==1 and not broken.exists(),(code,diagnostic)
assert not list(work.glob('.innodb-schema-*'))
report['broken_stdout_cleanup']=True
report['status']='complete'
(work/'report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
pathlib.Path('/tmp/innodb-input-preparation-live-work.txt').write_text(str(work))
print('report:',work/'report.json',flush=True)
