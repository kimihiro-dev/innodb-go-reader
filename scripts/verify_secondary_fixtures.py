#!/usr/bin/env python3
"""Offline official CRC/SDI and secondary CLI verification against saved SQL."""
import argparse,decimal,gzip,hashlib,json,os,subprocess,tempfile,base64
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql-bin',required=True);p.add_argument('--fixtures',default='testdata/secondary');a=p.parse_args();root=Path(a.fixtures)
manifest=json.loads((root/'manifest.json').read_text());assert manifest['status']=='captured'
def load(raw):return json.loads(raw,parse_float=decimal.Decimal)
def saved(name):return load(gzip.decompress((root/name).read_bytes()))
checks=[];physical=[];nrows=ndeleted=npages=0
env=os.environ.copy();env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage30-cache')
with tempfile.TemporaryDirectory(prefix='innodb-secondary-verify-') as tmp:
 tmp=Path(tmp)
 for name in ['secondary','sdi','materialized']:subprocess.run(['go','build','-o',str(tmp/name),'./examples/'+name],check=True,env=env)
 for case in manifest['cases']:
  name=case['name'];raw=gzip.decompress((root/(name+'.ibd.gz')).read_bytes());assert hashlib.sha256(raw).hexdigest()==case['sha256'];file=tmp/'snapshot.ibd';file.write_bytes(raw)
  subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(file)],check=True,capture_output=True)
  official=load(subprocess.check_output([str(Path(a.mysql_bin)/'ibd2sdi'),str(file)]));assert official==saved(name+'.sdi.json.gz')
  go_sdi=[load(line) for line in subprocess.check_output([str(tmp/'sdi'),str(file)]).splitlines()];assert go_sdi==official[1:]
  specs=saved(name+'.secondary.json.gz')
  for spec in specs:
   result=load(subprocess.check_output([str(tmp/'secondary'),str(file),spec['name']]))
   fields=result['Schema']['Fields'];actual=[]
   for r in result['Records']:
    row=[]
    for f,v in zip(fields,r['Values']):
     if f['RowID']:continue
     if v is not None and f['Definition']['type'] in ('BINARY','VARBINARY'):v=base64.b64decode(v).hex().upper()
     row.append(v)
    actual.append(row)
   assert actual==spec['rows'],(name,spec['name'])
   first=result['Records'][0] if result['Records'] else None
   sample=None
   if first:
    offset=first['PageNumber']*16384+first['Offset'];sample=dict(page=first['PageNumber'],origin=first['Offset'],start=first['Start'],end=first['End'],file_offset=offset,header=bytes(first['Header']).hex(),payload=raw[offset:offset+min(32,first['End']-first['Offset'])].hex())
   rows=len(actual);deleted=len(result['DeletedRecords'] or []);pages=len(result['Pages']);nrows+=rows;ndeleted+=deleted;npages+=pages
   physical.append(dict(table=name,index=spec['name'],rows=rows,deleted=deleted,pages=pages,level=result['Page']['Level'],sample=sample))
  checks.append(dict(name=name,sha256=case['sha256'],indexes=len(specs),crc32=True,sdi=True,cli_sql=True))
report=dict(files=len(checks),snapshot_rows=sum(c['rows'] for c in manifest['cases']),indexes=len(physical),records=nrows,deleted=ndeleted,pages=npages,tool_directory=a.mysql_bin,cases=checks)
(root/'verification.json').write_text(json.dumps(report,indent=2)+'\n');(root/'physical.json').write_text(json.dumps(physical,indent=2)+'\n')
print(f'{len(checks)} files; {len(physical)} indexes; {nrows} records; {ndeleted} delete marks; official CRC/SDI/CLI passed')
