import concurrent.futures,json,pathlib,subprocess,time
work=pathlib.Path('/tmp/innodb-57-patch-run');repo=pathlib.Path('/Users/kimihiro/workspace/codespace/go/src/innodb-go-reader')
versions=['5.7.9','5.7.10','5.7.11','5.7.12','5.7.28','5.7.43']
def capture(v,kind):
 private=work/('private-'+v+'.json')
 for _ in range(120):
  if private.exists():break
  time.sleep(1)
 else:raise RuntimeError('server not ready '+v)
 server=json.loads(private.read_text());out=work/(v+'-'+kind)
 if (out/'manifest.json').exists() and json.loads((out/'manifest.json').read_text()).get('status')=='captured':return v,kind,'previous capture complete'
 script='generate_mysql57_fixtures.py' if kind=='base' else 'generate_mysql57_secondary_fixtures.py'
 p=subprocess.run(['python3',str(repo/'scripts'/script),'--container',server['container'],'--out',str(out)],capture_output=True,text=True,timeout=600)
 (work/('capture-'+v+'-'+kind+'.log')).write_text(p.stdout+p.stderr)
 if p.returncode:raise RuntimeError(v+' '+kind+' '+(p.stdout+p.stderr)[-1800:])
 return v,kind,'captured'
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
 futures=[pool.submit(capture,v,k) for v in versions for k in ['base','secondary']]
 for future in concurrent.futures.as_completed(futures):print(*future.result(),flush=True)
