#!/usr/bin/env python3
"""Verify stage-33 snapshots and query CLI against saved SQL and official tools."""
import argparse,base64,decimal,gzip,hashlib,json,os,subprocess,tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql-bin',required=True);p.add_argument('--fixtures',default='testdata/query');a=p.parse_args()
out=Path(a.fixtures);manifest=json.loads((out/'manifest.json').read_text());assert manifest['status']=='captured'
env=dict(os.environ);env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage33-cache')
def decode(raw):return json.loads(raw,parse_float=decimal.Decimal)
checks=[];physical=[];query_count=0;query_rows=0
with tempfile.TemporaryDirectory(prefix='innodb-query-verify-') as work:
 work=Path(work)
 for example in ['query','materialized','sdi']:
  subprocess.run(['go','build','-o',str(work/example),'./examples/'+example],env=env,check=True)
 for case in manifest['cases']:
  name=case['name'];data=gzip.decompress((out/(name+'.ibd.gz')).read_bytes());assert hashlib.sha256(data).hexdigest()==case['sha256']
  file=work/'snapshot.ibd';file.write_bytes(data)
  subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(file)],check=True,capture_output=True)
  official=json.loads(subprocess.check_output([str(Path(a.mysql_bin)/'ibd2sdi'),str(file)]))
  assert official==json.loads(gzip.decompress((out/(name+'.sdi.json.gz')).read_bytes()))
  sdi=[json.loads(x) for x in subprocess.check_output([str(work/'sdi'),str(file)]).splitlines()];assert sdi==official[1:]
  full=decode(subprocess.check_output([str(work/'materialized'),str(file)]))['Result']
  assert [r['Values'] for r in full['Records']]==decode(gzip.decompress((out/(name+'.expected.json.gz')).read_bytes()))
  physical.append(dict(name=name,rows=len(full['Records']),pages=len(full['Pages']),level=full['Page']['Level'],external=sum(len(r['External'] or []) for r in full['Records'])))
  queries=json.loads(gzip.decompress((out/(name+'.queries.json.gz')).read_bytes()))
  for c in queries:
   spec=work/'query.json';spec.write_text(json.dumps(c['query'],ensure_ascii=False))
   result=subprocess.run([str(work/'query'),str(file),str(spec)],capture_output=True)
   assert result.returncode==0,(name,c['name'],result.stderr)
   lines=[decode(line) for line in result.stdout.splitlines()];report=lines[-1]['report']
   rows=[line['event']['Record']['Values'] for line in lines[:-1] if line['event']['Record'] is not None]
   assert report['Complete'] and rows==c['rows'],(name,c['name'])
   assert report['LimitReached']==bool(c['query'].get('Limit') and len(rows)==c['query']['Limit'])
   query_count+=1;query_rows+=len(rows)
  checks.append(dict(name=name,sha256=case['sha256'],rows=case['rows'],queries=len(queries),crc32=True,sdi=True,cli_sql=True))
 # Binary query JSON has an explicit base64 wrapper, independent of text keys.
 fixture=Path('testdata/compact/composite_compact_initial.ibd.gz');file=work/'binary.ibd';file.write_bytes(gzip.decompress(fixture.read_bytes()))
 full=decode(subprocess.check_output([str(work/'materialized'),str(file)]))['Result'];row=full['Records'][12]
 key=[dict(base64=row['Values'][1]),row['Values'][0]]
 spec=work/'binary-query.json';spec.write_text(json.dumps(dict(Lower=dict(Key=key,Inclusive=True),Upper=dict(Key=key,Inclusive=True))))
 lines=[decode(x) for x in subprocess.check_output([str(work/'query'),str(file),str(spec)]).splitlines()]
 assert lines[-1]['report']['Complete'] and [x['event']['Record']['Values'] for x in lines[:-1] if x['event']['Record'] is not None]==[row['Values']]
 # VIRTUAL retains an explicit opt-in; callback protocol is unchanged.
 file=work/'virtual.ibd';file.write_bytes(gzip.decompress(Path('testdata/generated/tree_initial.ibd.gz').read_bytes()))
 spec.write_text('{"Limit":1}')
 strict=subprocess.run([str(work/'query'),str(file),str(spec)],capture_output=True)
 assert strict.returncode and not decode(strict.stdout.splitlines()[-1])['report']['Complete']
 explicit=subprocess.run([str(work/'query'),'-materialized',str(file),str(spec)],capture_output=True)
 assert not explicit.returncode and decode(explicit.stdout.splitlines()[-1])['report']['Records']==1
report=dict(files=len(checks),rows=sum(c['rows'] for c in checks),queries=query_count,query_rows=query_rows,cases=checks,tool_directory=a.mysql_bin)
(out/'verification.json').write_text(json.dumps(report,indent=2)+'\n');(out/'physical.json').write_text(json.dumps(physical,indent=2)+'\n')
print(report['files'],'files;',report['rows'],'snapshot rows;',query_count,'queries;',query_rows,'SQL result rows; official CRC/SDI and CLI passed')
