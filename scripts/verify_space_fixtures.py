#!/usr/bin/env python3
"""Verify allocation snapshots against official CRC/page/SDI tools and saved SQL metadata."""
import argparse,collections,gzip,hashlib,json,os,re,struct,subprocess,tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--mysql-bin',required=True);p.add_argument('--fixtures',default='testdata/space');a=p.parse_args();root=Path(a.fixtures);m=json.loads((root/'manifest.json').read_text());assert m['status']=='captured';tools=Path(a.mysql_bin);env=os.environ.copy();env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage30-cache');checks=[]
def saved(path):return json.loads(gzip.decompress(path.read_bytes()))
def store(path,obj):path.write_bytes(gzip.compress((json.dumps(obj,indent=2)+'\n').encode(),mtime=0))
with tempfile.TemporaryDirectory(prefix='innodb-space-verify-') as tmp:
 tmp=Path(tmp)
 for name in ['space','sdi']:subprocess.run(['go','build','-o',str(tmp/name),'./examples/'+name],check=True,env=env)
 for c in m['cases']:
  name=c['name'];raw=gzip.decompress((root/(name+'.space.gz')).read_bytes());assert len(raw)==c['bytes'] and hashlib.sha256(raw).hexdigest()==c['sha256'];file=tmp/'snapshot.ibd';file.write_bytes(raw);dump=tmp/'pages.txt';dump.unlink(missing_ok=True)
  summary=subprocess.check_output([str(tools/'innochecksum'),'--strict-check=crc32','--page-type-summary','--page-type-dump='+str(dump),str(file)],text=True)
  official=json.loads(subprocess.check_output([str(tools/'ibd2sdi'),str(file)]));cli_sdi=[json.loads(x) for x in subprocess.check_output([str(tmp/'sdi'),str(file)]).splitlines()];assert cli_sdi==official[1:];store(root/(name+'.sdi.json.gz'),official)
  r=json.loads(subprocess.check_output([str(tmp/'space'),str(file)]));assert r['FilePages']==len(raw)//16384
  # Raw physical types include stale contents on now-free pages. Official type
  # counters do not establish current allocation and are compared separately.
  names={0:'Freshly allocated page',3:'Inode page',5:'Insert Buffer Bitmap',8:'File Space Header',9:'Extent descriptor page',10:'BLOB page',18:'SDI BLOB page',17853:'SDI Index page',17855:'Index page'}
  lines=[line for line in dump.read_text().splitlines() if line.startswith('#::')];other=sum(page['Type'] not in names for page in r['Pages']);assert len(lines)+other==r['FilePages'];assert int(re.search(r'(\d+)\s+Other type of page',summary).group(1))==other
  for line in lines:
   parts=line.split('|');n=int(parts[0][3:]);page=r['Pages'][n];assert parts[1].strip().lower()==names.get(page['Type'],'Other type of page').lower(),(name,n,parts[1],page['Type'])
   if page.get('Index') is not None:
    vals=list(map(int,re.search(r'index id=(\d+), page level=(\d+), No. of records=(\d+), garbage=(\d+)',parts[2]).groups()));idx=page['Index'];assert vals==[idx['IndexID'],idx['Level'],idx['Records'],idx['GarbageBytes']]
  counts=collections.Counter()
  for page in r['Pages']:
   n=page['Number']
   if n>=r['SizePages']:kind='file-tail'
   elif n>=r['FreeLimit']:kind='uninitialized'
   else:
    off=(n//16384)*16384*16384+150+((n%16384)//64)*40+24+(n%64)//4
    kind='free' if raw[off]&(1<<(2*(n%4))) else 'used'
   assert kind==page['Allocation'];counts[kind]+=1
  assert [counts[k] for k in ['used','free','uninitialized','file-tail']]==[r[k] for k in ['UsedPages','FreePages','UninitializedPages','TailPages']]
  sql=saved(root/(name+'.sql-metadata.json.gz'));assert sql['table'][0]['space']==r['SpaceID'];byid={i['ID']:i for i in r['Indexes']}
  for idx in sql['indexes']:assert byid[idx['id']]['Root']==idx['page'] and idx['space']==r['SpaceID']
  store(root/(name+'.analysis.json.gz'),r);(root/(name+'.official-pages.txt.gz')).write_bytes(gzip.compress(dump.read_bytes(),mtime=0));(root/(name+'.official-summary.txt')).write_text(summary.replace(str(file),'snapshot.ibd'))
  checks.append(dict(name=name,pages=r['FilePages'],used=r['UsedPages'],free=r['FreePages'],uninitialized=r['UninitializedPages'],zero=r['ZeroPages'],extents=len(r['Extents']),segments=len(r['Segments']),indexes=len(r['Indexes']),leased_extents=sum(e['State']==5 for e in r['Extents']),crc=True,sdi=True,page_dump=True,sql_metadata=True,independent_bitmap=True))
  print(name,'verified',r['FilePages'],'pages',flush=True)
(root/'verification.json').write_text(json.dumps(dict(files=len(checks),pages=sum(c['pages'] for c in checks),cases=checks),indent=2)+'\n')
