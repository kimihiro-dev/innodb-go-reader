#!/usr/bin/env python3
"""Verify immutable partition snapshots using SQL, official tools and the public CLI."""
import argparse, csv, gzip, hashlib, io, json, os, subprocess, tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--mysql-bin',required=True)
p.add_argument('--fixtures',default='testdata/partitions')
a=p.parse_args();root=Path(a.fixtures);m=json.loads((root/'manifest.json').read_text());assert m['status']=='captured'
env=os.environ.copy();env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage30-cache')
def load(name):return json.loads(gzip.decompress((root/name).read_bytes()))
def store(name,value):(root/name).write_bytes(gzip.compress((json.dumps(value,indent=2)+'\n').encode(),mtime=0))
def decode(cell):
    if cell['type']=='null':return None
    if cell['type'] in ('int32','int64'):return int(cell['value'])
    if cell['type']=='string':return cell['value']
    raise AssertionError(cell['type'])
checks=[]
with tempfile.TemporaryDirectory(prefix='partition-verify-') as temp:
    temp=Path(temp);binary=temp/'innodb-reader'
    subprocess.run(['go','build','-o',str(binary),'./cmd/innodb-reader'],env=env,check=True)
    for case in m['cases']:
        directory=temp/case['name'];directory.mkdir();files=[];objects=0;table_sdi=None
        for i,f in enumerate(case['files']):
            raw=gzip.decompress((root/f['file']).read_bytes());assert len(raw)==f['bytes'] and hashlib.sha256(raw).hexdigest()==f['sha256']
            file=directory/('file-'+str(i)+'.ibd');file.write_bytes(raw)
            subprocess.run([str(Path(a.mysql_bin)/'innochecksum'),'--strict-check=crc32',str(file)],check=True,capture_output=True)
            official=json.loads(subprocess.check_output([str(Path(a.mysql_bin)/'ibd2sdi'),str(file)]));objects+=len(official)-1
            for obj in official[1:]:
                if obj['object']['dd_object_type']=='Table': table_sdi=obj['object']['dd_object']
            store(f['file'].removesuffix('.partition.gz')+'.sdi.json.gz',official)
            files.append({'partition':f['partition'],'path':file.name})
        manifest=directory/'files.json';manifest.write_text(json.dumps({'version':1,'files':list(reversed(files))}))
        meta=subprocess.run([str(binary),'metadata','--manifest',str(manifest)],capture_output=True,text=True)
        if not case['supported']:
            assert meta.returncode==1 and not meta.stdout and 'subpartition' in meta.stderr
            checks.append({'name':case['name'],'files':len(files),'sdi_objects':objects,'crc':True,'rejected':True});continue
        assert meta.returncode==0,meta.stderr
        report=json.loads(meta.stdout);sql=load(case['name']+'.sql-metadata.json.gz');expected=load(case['name']+'.expected.json.gz')
        tables={v['name']:v for v in sql['tables']}
        assert table_sdi is not None
        parts={p['name']:p for p in table_sdi['partitions']}
        assert [(v['Source']['Name'],v['Source']['Number']) for v in report['Partitions']]==[(v['name'],v['ordinal']-1) for v in sql['partitions']]
        assert {'HASH':1,'KEY':3,'RANGE':7,'LIST':8}[sql['partitions'][0]['method']]==report['PartitionType']
        assert sql['partitions'][0]['expression']==table_sdi['partition_expression_utf8']
        assert report['Expression']==table_sdi['partition_expression']
        def properties(raw): return dict(item.split('=',1) for item in raw.split(';') if item)
        # INNODB_INDEXES scans mysql.indexes and skips empty se_private_data.
        # Partition physical index identities come from official ibd2sdi instead.
        for partition in report['Partitions']:
            source=partition['Source'];name=m['database']+'/'+case['table']+'#p#'+source['Name']
            assert source['TableID']==tables[name]['table_id'] and source['SpaceID']==tables[name]['space']
            for index in partition['Table']['Indexes']:
                opx=next(i for i,v in enumerate(table_sdi['indexes']) if v['name']==index['Name'])
                oracle=properties(next(v for v in parts[source['Name']]['indexes'] if v['index_opx']==opx)['se_private_data'])
                assert (index['ID'],index['RootPage'],index['SpaceID'])==tuple(int(oracle[k]) for k in ('id','root','space_id'))
        count=0
        for format in ('jsonl','csv'):
            proc=subprocess.run([str(binary),'export','--manifest',str(manifest),'--materialized','--physical','--format',format],capture_output=True,text=True)
            assert proc.returncode==0,proc.stderr
            if format=='jsonl':
                events=[json.loads(line) for line in proc.stdout.splitlines()];assert events[0]['kind']=='schema' and events[-1]=={'kind':'end','rows':str(case['rows'])}
                rows=[([decode(v) for v in e['values']],e['physical']) for e in events[1:-1]]
            else:
                records=list(csv.reader(io.StringIO(proc.stdout)));header=json.loads(records[0][0]);assert header['schema']['physical']
                rows=[([decode(json.loads(v)) for v in record[:-1]],json.loads(record[-1])) for record in records[1:]]
            per={f['partition']:[] for f in case['files']};last=-1
            for row,source in rows:
                part=source['partition'];assert part['Number']>=last;last=part['Number'];per[part['Name']].append(row)
                assert 'Values' not in source['record']
            assert per==expected['partitions']
            assert sorted(json.dumps(row) for row,_ in rows)==sorted(json.dumps(row) for row in expected['rows'])
            count=len(rows)
        check=subprocess.run([str(binary),'check','--manifest',str(manifest),'--materialized'],capture_output=True,text=True);assert check.returncode==0,check.stderr
        assert json.loads(check.stdout)['CompletedPartitions']==len(files)
        checks.append({'name':case['name'],'files':len(files),'sdi_objects':objects,'crc':True,'sql_table_identity':True,'official_index_identity':True,'jsonl_csv_rows':count,'check':True})
        print(case['name'],'verified',len(files),'files',count,'rows',flush=True)
(root/'verification.json').write_text(json.dumps({'files':sum(c['files'] for c in checks),'sdi_objects':sum(c['sdi_objects'] for c in checks),'rows_per_format':sum(c.get('jsonl_csv_rows',0) for c in checks),'cases':checks},indent=2)+'\n')
