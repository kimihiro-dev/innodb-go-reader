#!/usr/bin/env python3
"""Offline typed-key/clustered-index verification; supply official MySQL bin directory."""
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
p.add_argument('--fixtures',default='testdata/keys')
a=p.parse_args()
out=Path(a.fixtures)
manifest=json.loads((out/'manifest.json').read_text())
assert manifest['status']=='captured'
env=dict(os.environ,GOCACHE='/tmp/innodb-go-build-cache')
checks=[]
with tempfile.TemporaryDirectory(prefix='innodb-key-verify-') as work:
    work=Path(work)
    for example in ('auto','sdi','json'):
        subprocess.run(['go','build','-o',str(work/example),'./examples/'+example],env=env,check=True)
    for c in manifest['cases']:
        name=c['name']; snapshot=work/'table.ibd'
        data=gzip.decompress((out/(name+'.ibd.gz')).read_bytes())
        assert hashlib.sha256(data).hexdigest()==c['sha256']
        snapshot.write_bytes(data)
        subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(snapshot)],check=True,capture_output=True)
        raw=subprocess.run([str(Path(a.mysql_bin)/'ibd2sdi'),str(snapshot)],check=True,capture_output=True).stdout
        (out/(name+'.sdi.json.gz')).write_bytes(gzip.compress(raw,mtime=0))
        actual=[json.loads(x) for x in subprocess.run([str(work/'sdi'),str(snapshot)],check=True,capture_output=True).stdout.splitlines()]
        assert actual==json.loads(raw)[1:],name+' SDI'
        schema=json.loads((out/(name+'.json')).read_text())
        example='json' if any(c['type']=='JSON' for c in schema['columns']) else 'auto'
        rows=[json.loads(x,parse_float=decimal.Decimal) for x in subprocess.run([str(work/example),str(snapshot)],check=True,capture_output=True).stdout.splitlines()]
        for row in rows:
            for i,col in enumerate(schema['columns']):
                if (col['type'] in ('BINARY','VARBINARY') or col['type'].endswith('BLOB')) and row[i] is not None:
                    row[i]=base64.b64decode(row[i]).hex().upper()
        expected=json.loads(gzip.decompress((out/(name+'.expected.json.gz')).read_bytes()),parse_float=decimal.Decimal)
        if c.get('unordered'):
            from collections import Counter
            assert Counter(json.dumps(r,ensure_ascii=False) for r in rows)==Counter(json.dumps(r,ensure_ascii=False) for r in expected),name+' SQL multiset'
        else:
            assert rows==expected,name+' SQL'
        checks.append(dict(name=name,rows=len(rows),sha256=c['sha256'],crc32=True,sdi=True,cli_sql=True))
report=dict(cases=checks,files=len(checks),rows=sum(c['rows'] for c in checks),tool_directory=a.mysql_bin)
(out/'verification.json').write_text(json.dumps(report,indent=2)+'\n')
print(report['files'],'files;',report['rows'],'SQL rows; CRC/SDI/CLI passed')
