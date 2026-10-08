#!/usr/bin/env python3
"""Verify stage-35 SQL queries using official tools and the projection CLI."""
import argparse,gzip,hashlib,json,os,subprocess,tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql-bin',required=True);p.add_argument('--fixtures',default='testdata/secondary_query');a=p.parse_args();root=Path(a.fixtures);manifest=json.loads((root/'manifest.json').read_text());assert manifest['status']=='captured'
def saved(name):return json.loads(gzip.decompress((root/name).read_bytes()))
checks=[];queries=rows=covered=lookups=0;env=os.environ.copy();env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage30-cache')
with tempfile.TemporaryDirectory(prefix='secondaryquery-verify-') as tmp:
 tmp=Path(tmp)
 for name in ['secondaryquery','sdi','materialized']:subprocess.run(['go','build','-o',str(tmp/name),'./examples/'+name],check=True,env=env)
 def run(file,index,spec,materialized=False):
  path=tmp/'query.json';path.write_text(json.dumps(spec,ensure_ascii=False));cmd=[str(tmp/'secondaryquery'),'-max-entries=10000000']
  if materialized:cmd.append('-materialized')
  result=subprocess.run(cmd+[str(file),index,str(path)],capture_output=True);lines=[json.loads(x) for x in result.stdout.splitlines()];return result,lines
 for case in manifest['cases']:
  name=case['name'];raw=gzip.decompress((root/(name+'.ibd.gz')).read_bytes());assert hashlib.sha256(raw).hexdigest()==case['sha256'];file=tmp/'snapshot.ibd';file.write_bytes(raw)
  subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(file)],capture_output=True,check=True)
  official=json.loads(subprocess.check_output([str(Path(a.mysql_bin)/'ibd2sdi'),str(file)]));assert official==saved(name+'.sdi.json.gz')
  assert [json.loads(x) for x in subprocess.check_output([str(tmp/'sdi'),str(file)]).splitlines()]==official[1:]
  for spec in saved(name+'.queries.json.gz'):
   result,lines=run(file,'s',spec['query']);assert result.returncode==0,(name,result.stderr)
   report=lines[-1]['report'];assert report['Complete'];actual=[x['row']['Values'] for x in lines[:-1]];assert actual==spec['rows'],(name,spec['query'])
   assert report['LimitReached']==bool(spec['query']['Range'].get('Limit') and len(actual)==spec['query']['Range']['Limit'])
   assert report['PageReads']==report['CacheHits']+report['PhysicalReads'];queries+=1;rows+=len(actual);covered+=report['Covered'];lookups+=report['Lookups']
  if name=='huge':
   result,lines=run(file,'s',dict(Range=dict(Lower=dict(Key=[1],Inclusive=True),Upper=dict(Key=[1],Inclusive=True)),Columns=['payload']));assert result.returncode and not lines[-1]['report']['Complete'] and 'unsupported' in lines[-1]['error']
  checks.append(dict(name=name,sha256=case['sha256'],queries=case['queries'],crc32=True,sdi=True,cli_sql=True))
 # Binary wrappers carry full original column bytes, even for a prefix index.
 file=tmp/'binary.ibd';file.write_bytes(gzip.decompress(Path('testdata/secondary/compact.ibd.gz').read_bytes()));full=json.loads(subprocess.check_output([str(tmp/'materialized'),str(file)]));r=full['Result']['Records'][0]
 key=[dict(base64=r['Values'][3]),dict(base64=r['Values'][4])];result,lines=run(file,'b_idx',dict(Range=dict(Lower=dict(Key=key,Inclusive=True),Upper=dict(Key=key,Inclusive=True)),Columns=['id']));assert not result.returncode and [x['row']['Values'][0] for x in lines[:-1]]==list(range(80))
 file=tmp/'virtual.ibd';file.write_bytes(gzip.decompress(Path('testdata/secondary/generated.ibd.gz').read_bytes()));spec=dict(Range=dict(Limit=1),Columns=['g']);result,lines=run(file,'g_idx',spec);assert result.returncode and not lines[-1]['report']['Complete'];result,lines=run(file,'g_idx',spec,True);assert not result.returncode and lines[-1]['report']['VirtualColumns'] and lines[-1]['report']['LimitReached']
report=dict(files=len(checks),queries=queries,rows=rows,covered=covered,lookups=lookups,cases=checks,tool_directory=a.mysql_bin);(root/'verification.json').write_text(json.dumps(report,indent=2)+'\n');print(f'{len(checks)} files; {queries} SQL queries; {rows} rows; {covered} covered outputs; {lookups} lookups; CRC/SDI/CLI passed')
