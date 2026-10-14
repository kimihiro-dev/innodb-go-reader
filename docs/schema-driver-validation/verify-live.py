import gzip,hashlib,json,os,pathlib,socket,subprocess,sys,threading,tempfile,base64,signal,time
repo=pathlib.Path('/Users/kimihiro/workspace/codespace/go/src/innodb-go-reader')
sys.path.insert(0,str(repo/'scripts'))
from mysql57_schema import Client,collect,collect_indexes,snapshot
server_work=pathlib.Path(pathlib.Path('/tmp/innodb-driver-live-work.txt').read_text());private=json.loads((server_work/'private.json').read_text());work=pathlib.Path(tempfile.mkdtemp(prefix='run-',dir=server_work));pathlib.Path('/tmp/innodb-driver-verified-work.txt').write_text(str(work))
reader='/tmp/innodb-driver-reader'
password=private['password']
report={'status':'incomplete','container':private['container'],'host':private['host'],'port':private['port'],'cases':[]}
base=['--user','schema_reader','--password-env','INNODB_DRIVER_TEST_PASSWORD','--database','lesson','--indexes']
def run(args,name,success=True,env_override=None):
 env=os.environ.copy();env.update(PATH='',INNODB_DRIVER_TEST_PASSWORD=password)
 if env_override:env.update(env_override)
 p=subprocess.run([reader,*args],env=env,capture_output=True,text=True,timeout=60)
 assert password not in p.stdout+p.stderr,'credential disclosure'
 (work/(name+'.stdout')).write_text(p.stdout);(work/(name+'.stderr')).write_text(p.stderr)
 assert (p.returncode==0)==success,(name,p.returncode,p.stderr)
 if p.stderr:json.loads(p.stderr)
 return p

def normalize(v):
 if isinstance(v,dict):
  v={k:normalize(x) for k,x in v.items()}
  if v.get('max_chars')==0:v.pop('max_chars')
  if v.get('nullable') is False:v.pop('nullable')
  return v
 if isinstance(v,list):return [normalize(x) for x in v]
 return v

# An actual Unix socket forwards bytes to the same real MySQL backend.
# This tests driver Unix transport, not a separate native-server installation.
sockpath=pathlib.Path('/tmp/innodb-driver-proxy-'+str(os.getpid())+'.sock');listener=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);listener.bind(str(sockpath));listener.listen();stopping=threading.Event()
def relay(src,dst):
 try:
  while True:
   data=src.recv(65536)
   if not data:break
   dst.sendall(data)
 except OSError:pass
 finally:
  try:dst.shutdown(socket.SHUT_WR)
  except OSError:pass

def accept():
 while not stopping.is_set():
  try:client,_=listener.accept()
  except OSError:return
  target=socket.create_connection((private['host'],private['port']))
  def session(c=client,t=target):
   thread=threading.Thread(target=relay,args=(c,t),daemon=True);thread.start();relay(t,c);thread.join();c.close();t.close()
  threading.Thread(target=session,daemon=True).start()
threading.Thread(target=accept,daemon=True).start()
try:
 for table in ['t','hidden','generated']:
  reference=work/(table+'-reference');reference.mkdir()
  mysql=Client(private['container'])
  try:
   mysql.query('FLUSH TABLES `lesson`.`'+table+'` FOR EXPORT')
   schema,evidence=collect(mysql,'lesson',table);indexes=collect_indexes(mysql,schema,evidence);raw=snapshot(mysql,evidence)
   expressions={'t':"JSON_ARRAY(id,n,name,e,s,HEX(vb))",'hidden':"JSON_ARRAY(n,name)",'generated':"JSON_ARRAY(id,n,g)"}
   expected=mysql.objects(expressions[table],'FROM `lesson`.`'+table+'`')
  finally:mysql.query('UNLOCK TABLES');mysql.close()
  for name,value in [('schema.json',schema),('indexes.json',indexes),('source.json',evidence),('expected.json',expected)]:
   (reference/name).write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n')
  (reference/'table.ibd.gz').write_bytes(gzip.compress(raw,mtime=0));(reference/'table.ibd').write_bytes(raw)
  modes={'tcp':['--host',private['host'],'--port',str(private['port'])], 'unix':['--socket',str(sockpath)], 'tls':['--host',private['host'],'--port',str(private['port']),'--tls','--tls-ca',private['ca']]}
  for mode,connection in modes.items():
   out=work/(table+'-'+mode)
   run(['schema',*connection,*base,'--table',table,'--output',str(out)],table+'-'+mode)
   assert normalize(json.loads((out/'schema.json').read_text()))==normalize(schema)
   assert normalize(json.loads((out/'indexes.json').read_text()))==normalize(indexes)
   actual=json.loads((out/'source.json').read_text())
   for key,value in evidence.items():assert actual[key]==value,(table,mode,key)
   assert actual['connection']['network']==('unix' if mode=='unix' else 'tcp') and actual['connection']['tls']==(mode=='tls')
   assert len(list(out.iterdir()))==3,'unexpected snapshot product'
   flags=['--schema',str(out/'schema.json')]
   materialized=['--materialized'] if schema.get('virtual_columns') else []
   run(['metadata',*flags,'--indexes',str(out/'indexes.json'),str(reference/'table.ibd')],table+'-'+mode+'-metadata')
   p=run(['export',*flags,*materialized,str(reference/'table.ibd')],table+'-'+mode+'-export')
   records=[json.loads(line) for line in p.stdout.splitlines()];assert records[-1]['kind']=='end'
   actual_rows=[]
   for record in records:
    if record['kind']!='row':continue
    row=[]
    for cell in record['values']:
     value=cell.get('value')
     if cell['type'].startswith(('int','uint')):value=int(value)
     elif cell['type']=='binary':value=base64.b64decode(value).hex().upper()
     row.append(value)
    actual_rows.append(row)
   assert actual_rows==expected,(table,mode,actual_rows,expected)
   for index in indexes:run(['check','--scope','secondary',*flags,'--indexes',str(out/'indexes.json'),'--index',index['Name'],str(reference/'table.ibd')],table+'-'+mode+'-'+index['Name'])
   report['cases'].append({'table':table,'transport':mode,'models_and_source_match':True,'export_rows':len(expected),'secondary_indexes':len(indexes)})
   print(table,mode,'verified',flush=True)
 connection=['--host',private['host'],'--port',str(private['port'])]
 failures=[('wrong-password',base,connection,{'INNODB_DRIVER_TEST_PASSWORD':'incorrect'}),('no-process',[x if x!='schema_reader' else 'no_process' for x in base],connection,None),('untrusted-tls',base,[*connection,'--tls'],None),('missing-table',base,connection,None)]
 for name,args,conn,env in failures:
  dest=work/name;table='absent' if name=='missing-table' else 't'
  run(['schema',*conn,*args,'--table',table,'--output',str(dest)],name,False,env)
  assert not dest.exists(),'published failing collection'
 assert not list(work.glob('.innodb-schema-*'))
 # Silent sockets reproduce stalled handshake timeout and SIGINT cancellation.
 for name in ['timeout','cancel']:
  stallpath=pathlib.Path('/tmp/innodb-driver-stall-'+str(os.getpid())+'.sock')
  stall=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);stall.bind(str(stallpath));stall.listen()
  accepted=threading.Event()
  def silent():
   client,_=stall.accept();accepted.set();time.sleep(1);client.close()
  thread=threading.Thread(target=silent,daemon=True);thread.start()
  dest=work/name;env=os.environ.copy();env.update(PATH='',INNODB_DRIVER_TEST_PASSWORD=password)
  args=[reader,'schema','--socket',str(stallpath),*base,'--table','t','--output',str(dest),'--timeout','100ms' if name=='timeout' else '5s']
  p=subprocess.Popen(args,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
  assert accepted.wait(5)
  if name=='cancel':p.send_signal(signal.SIGINT)
  stdout,stderr=p.communicate(timeout=5)
  assert password not in stdout+stderr and p.returncode==130,(name,p.returncode,stderr)
  assert not dest.exists() and not list(work.glob('.innodb-schema-*'))
  (work/(name+'.stdout')).write_text(stdout);(work/(name+'.stderr')).write_text(stderr)
  stall.close();thread.join();stallpath.unlink()
 report['negative_checks']=[x[0] for x in failures]+['timeout','cancel']
 report['credentials_absent_from_outputs']=True
 report['status']='complete'
 (work/'report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
 print('report:',work/'report.json',flush=True)
finally:
 stopping.set();listener.close()
 try:sockpath.unlink()
 except OSError:pass
