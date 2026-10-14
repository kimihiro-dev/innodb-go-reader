import argparse,gzip,hashlib,json,pathlib,subprocess,tarfile,urllib.request,ssl
parser=argparse.ArgumentParser();parser.add_argument('versions',nargs='+');args=parser.parse_args()
work=pathlib.Path('/tmp/innodb-mysql-patch-images');work.mkdir(exist_ok=True)
repository='library/mysql'
def request(url,headers=None):
 return urllib.request.urlopen(urllib.request.Request(url,headers=headers or {}),timeout=120,context=ssl.create_default_context(cafile="/etc/ssl/cert.pem"))
token=json.load(request('https://auth.docker.io/token?service=registry.docker.io&scope=repository:'+repository+':pull'))['token']
headers={'Authorization':'Bearer '+token,'Accept':'application/vnd.docker.distribution.manifest.v1+json, application/vnd.docker.distribution.manifest.v1+prettyjws'}
for version in args.versions:
 versiondir=work/version;versiondir.mkdir(exist_ok=True)
 with request('https://registry-1.docker.io/v2/'+repository+'/manifests/'+version,headers) as response:
  manifest=json.load(response);digest=response.headers.get('Docker-Content-Digest')
 assert manifest.get('schemaVersion')==1,manifest.get('schemaVersion')
 (versiondir/'source-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
 history=[json.loads(h['v1Compatibility']) for h in manifest['history']]
 diffs=[];layers=[];imagehistory=[]
 for layer,old in reversed(list(zip(manifest['fsLayers'],history))):
  imagehistory.append({'created':old['created'],'created_by':' '.join(old.get('container_config',{}).get('Cmd',[])),**({'empty_layer':True} if old.get('throwaway') else {})})
  if old.get('throwaway'):continue
  blob=layer['blobSum'];compressed=work/(blob.replace(':','-')+'.gz');unpacked=work/(blob.replace(':','-')+'.tar')
  if not compressed.exists():
   print(version,'download',blob,flush=True)
   with request('https://registry-1.docker.io/v2/'+repository+'/blobs/'+blob,headers) as response,compressed.open('wb') as out:
    while chunk:=response.read(1024*1024):out.write(chunk)
  assert 'sha256:'+hashlib.sha256(compressed.read_bytes()).hexdigest()==blob
  if not unpacked.exists():
   with gzip.open(compressed,'rb') as src,unpacked.open('wb') as out:
    while chunk:=src.read(1024*1024):out.write(chunk)
  diff=hashlib.sha256(unpacked.read_bytes()).hexdigest();diffs.append('sha256:'+diff);layers.append((unpacked,diff+'/layer.tar'))
 top=history[0];config={k:top[k] for k in ['created','architecture','os','config'] if k in top};config.setdefault('os','linux');config['rootfs']={'type':'layers','diff_ids':diffs};config['history']=imagehistory
 configdata=json.dumps(config,separators=(',',':')).encode();configname=hashlib.sha256(configdata).hexdigest()+'.json';(versiondir/configname).write_bytes(configdata)
 tag='innodb-reader/mysql-patch:'+version
 docker_manifest=[{'Config':configname,'RepoTags':[tag],'Layers':[name for _,name in layers]}]
 (versiondir/'manifest.json').write_text(json.dumps(docker_manifest))
 archive=versiondir/'docker-image.tar'
 with tarfile.open(archive,'w') as out:
  out.add(versiondir/configname,arcname=configname);out.add(versiondir/'manifest.json',arcname='manifest.json')
  for path,name in layers:out.add(path,arcname=name)
 p=subprocess.run(['docker','load','-i',str(archive)],capture_output=True,text=True,timeout=180)
 assert p.returncode==0,p.stderr
 archive.unlink()
 report={'source':'docker.io/library/mysql:'+version,'source_manifest_digest':digest,'conversion':'schema1 to local Docker archive; layer bytes verified against source digests; no server binaries changed','tag':tag,'layer_diff_ids':diffs,'image':json.loads(subprocess.run(['docker','image','inspect',tag],check=True,capture_output=True,text=True).stdout)[0]['Id']}
 (versiondir/'report.json').write_text(json.dumps(report,indent=2)+'\n')
 print(version,'loaded',report['image'],flush=True)
