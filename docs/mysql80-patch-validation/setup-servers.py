import pathlib,subprocess,json,time,secrets,sys
out=pathlib.Path('/tmp/innodb-80-run');out.mkdir(exist_ok=True)
for v in sys.argv[1:]:
 p=out/('server-'+v+'.json')
 if p.exists():continue
 name='innodb-80-'+v.replace('.','-')+'-'+secrets.token_hex(3)
 image='mysql:'+v
 subprocess.run(['docker','run','-d','--platform','linux/amd64','--name',name,'-e','MYSQL_ALLOW_EMPTY_PASSWORD=yes',image,'--innodb-buffer-pool-size=64M','--innodb-checksum-algorithm=strict_crc32','--character-set-server=utf8mb4','--collation-server=utf8mb4_bin','--skip-log-bin'],check=True,capture_output=True)
 for i in range(120):
  r=subprocess.run(['docker','exec',name,'mysql','--no-defaults','-uroot','--protocol=tcp','-h127.0.0.1','-NBe','SELECT VERSION()'],capture_output=True,text=True)
  if r.returncode==0:break
  time.sleep(1)
 else:raise RuntimeError(subprocess.run(['docker','logs',name],capture_output=True,text=True).stdout[-2000:])
 assert r.stdout.strip().split('-')[0]==v
 info=json.loads(subprocess.run(['docker','inspect',name],check=True,capture_output=True,text=True).stdout)[0]
 img=json.loads(subprocess.run(['docker','image','inspect',image],check=True,capture_output=True,text=True).stdout)[0]
 p.write_text(json.dumps(dict(version=v,actual_version=r.stdout.strip(),container=name,image=image,image_id=info['Image'],repo_digests=img.get('RepoDigests')),indent=2)+'\n')
 print(v,'ready',name,flush=True)
