#!/usr/bin/env python3
"""Offline COMPACT / BLOB verification; supply official MySQL bin directory."""
import argparse
import base64
import decimal
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--mysql-bin',required=True)
p.add_argument('--fixtures',default='testdata/compact')
a=p.parse_args()
out=Path(a.fixtures)
manifest=json.loads((out/'manifest.json').read_text())
assert manifest['status']=='captured'
env=dict(os.environ,GOCACHE='/private/tmp/innodb-go-reader-stage30-cache')
checks=[]
physical=[]
with tempfile.TemporaryDirectory(prefix='innodb-generated-verify-') as work:
    work=Path(work)
    for example in ('auto','sdi','materialized','json'):
        subprocess.run(['go','build','-o',str(work/example),'./examples/'+example],env=env,check=True)
    for c in manifest['cases']:
        name=c['name']; snapshot=work/'table.ibd'
        data=gzip.decompress((out/(name+'.ibd.gz')).read_bytes())
        assert hashlib.sha256(data).hexdigest()==c['sha256']
        snapshot.write_bytes(data)
        subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(snapshot)],check=True,capture_output=True)
        raw=subprocess.run([str(Path(a.mysql_bin)/'ibd2sdi'),str(snapshot)],check=True,capture_output=True).stdout
        assert json.loads(raw)==json.loads(gzip.decompress((out/(name+'.sdi.json.gz')).read_bytes())),name+' stored official SDI'
        actual=[json.loads(x) for x in subprocess.run([str(work/'sdi'),str(snapshot)],check=True,capture_output=True).stdout.splitlines()]
        assert actual==json.loads(raw)[1:],name+' SDI'
        schema=json.loads((out/(name+'.json')).read_text())
        strict=subprocess.run([str(work/'auto'),str(snapshot)],capture_output=True)
        run=subprocess.run([str(work/'materialized'),str(snapshot)],capture_output=True)
        if c.get('reject'):
            assert strict.returncode and run.returncode and not strict.stdout and not run.stdout,name+' rejection'
            checks.append(dict(name=name,rows=0,sha256=c['sha256'],crc32=True,sdi=True,cli_rejection=True))
            continue
        assert run.returncode==0,run.stderr
        report=json.loads(run.stdout,parse_float=decimal.Decimal)
        result=report['Result']
        records=result['Records']
        physical.append(dict(name=name,rows=len(records),pages=len(result['Pages']),level=result['Page']['Level'],external=sum(len(r['External'] or []) for r in records),deleted=len(result['DeletedRecords'] or [])))
        rows=[r['Values'] for r in records]
        assert len(report['Columns'])==len(schema['columns'])
        assert all(all(actual.get(k)==v for k,v in wanted.items()) for actual,wanted in zip(report['Columns'],schema['columns']))
        assert (report['VirtualColumns'] or [])==schema.get('virtual_columns',[])
        if schema.get('virtual_columns'):
            assert strict.returncode and not strict.stdout,name+' strict refusal'
        else:
            assert strict.returncode==0,strict.stderr
            assert [json.loads(x,parse_float=decimal.Decimal) for x in strict.stdout.splitlines()]==rows
        for row in rows:
            for i,col in enumerate(schema['columns']):
                if (col['type'] in ('BINARY','VARBINARY') or col['type'].endswith('BLOB')) and row[i] is not None:
                    row[i]=base64.b64decode(row[i]).hex().upper()
        if any(col['type']=='JSON' for col in schema['columns']):
            rows=[json.loads(x,parse_float=decimal.Decimal) for x in subprocess.run([str(work/'json'),str(snapshot)],check=True,capture_output=True).stdout.splitlines()]
            for row in rows:
                for i,col in enumerate(schema['columns']):
                    if (col['type'] in ('BINARY','VARBINARY') or col['type'].endswith('BLOB')) and row[i] is not None:
                        row[i]=base64.b64decode(row[i]).hex().upper()
        expected=json.loads(gzip.decompress((out/(name+'.expected.json.gz')).read_bytes()),parse_float=decimal.Decimal)
        for row in expected:
            for i,col in enumerate(schema['columns']):
                if col['type']=='JSON' and row[i] is not None:
                    row[i]=json.loads(row[i],parse_float=decimal.Decimal)
        if c.get('unordered'):
            from collections import Counter
            assert Counter(json.dumps(r,ensure_ascii=False) for r in rows)==Counter(json.dumps(r,ensure_ascii=False) for r in expected),name+' SQL multiset'
        else:
            assert rows==expected,name+' SQL'
        checks.append(dict(name=name,rows=len(rows),sha256=c['sha256'],crc32=True,sdi=True,cli_sql=True))
report=dict(cases=checks,files=len(checks),rows=sum(c['rows'] for c in checks),tool_directory=a.mysql_bin)
(out/'physical.json').write_text(json.dumps(physical,indent=2)+'\n')
(out/'verification.json').write_text(json.dumps(report,indent=2)+'\n')
print(report['files'],'files;',report['rows'],'SQL rows; CRC/SDI/CLI passed')
