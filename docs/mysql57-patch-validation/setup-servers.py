import json,pathlib,secrets,subprocess,tempfile,time,sys
work=pathlib.Path('/tmp/innodb-57-patch-run');work.mkdir(exist_ok=True);certs=work/'certs';certs.mkdir(exist_ok=True)
def run(args,**kw):
 p=subprocess.run(args,capture_output=True,timeout=60,**kw)
 if p.returncode:raise RuntimeError(p.stderr.decode()[:1600])
 return p.stdout
if not (certs/'server-cert.pem').exists():
 openssl='/opt/homebrew/bin/openssl'
 run([openssl,'req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(certs/'ca-key.pem'),'-out',str(certs/'ca.pem'),'-days','7','-subj','/CN=innodb patch test CA'])
 run([openssl,'req','-newkey','rsa:2048','-nodes','-keyout',str(certs/'server-key.pem'),'-out',str(certs/'server.csr'),'-subj','/CN=localhost'])
 (certs/'extensions.cnf').write_text('subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n')
 run([openssl,'x509','-req','-in',str(certs/'server.csr'),'-CA',str(certs/'ca.pem'),'-CAkey',str(certs/'ca-key.pem'),'-CAcreateserial','-out',str(certs/'server-cert.pem'),'-days','7','-extfile',str(certs/'extensions.cnf')])
 for f in ['ca.pem','server-key.pem','server-cert.pem']:(certs/f).chmod(0o644)
for version in sys.argv[1:]:
 private=work/('private-'+version+'.json')
 if private.exists():continue
 name='innodb-patch-'+version.replace('.','-')+'-'+secrets.token_hex(3)
 image=('innodb-reader/mysql-patch:' if int(version.split('.')[-1])<28 else 'mysql:')+version
 run(['docker','run','-d','--platform','linux/amd64','--name',name,'-e','MYSQL_ALLOW_EMPTY_PASSWORD=yes','-p','127.0.0.1::3306','-v',str(certs)+':/driver-certs:ro',image,'--innodb-buffer-pool-size=64M','--innodb-checksum-algorithm=strict_crc32','--character-set-server=utf8mb4','--collation-server=utf8mb4_bin','--ssl-ca=/driver-certs/ca.pem','--ssl-cert=/driver-certs/server-cert.pem','--ssl-key=/driver-certs/server-key.pem','--skip-log-bin'])
 for attempt in range(60):
  p=subprocess.run(['docker','exec',name,'mysql','--no-defaults','-uroot','--protocol=tcp','-h127.0.0.1','--batch','--skip-column-names','-e','SELECT VERSION()'],capture_output=True)
  if p.returncode==0:break
  time.sleep(1)
 else:raise RuntimeError('failed startup '+name+' '+run(['docker','logs',name]).decode()[-1000:])
 actual=p.stdout.decode().strip();assert actual.split('-')[0]==version,(actual,version)
 password=secrets.token_hex(20)
 sql="CREATE USER 'schema_reader'@'%' IDENTIFIED BY '"+password+"'; GRANT SELECT,PROCESS ON *.* TO 'schema_reader'@'%';"
 run(['docker','exec','-i',name,'mysql','--no-defaults','-uroot'],input=sql.encode())
 info=json.loads(run(['docker','inspect',name]))[0];port=int(info['NetworkSettings']['Ports']['3306/tcp'][0]['HostPort'])
 data={'version':version,'actual_version':actual,'container':name,'image':image,'image_id':info['Image'],'host':'127.0.0.1','port':port,'password':password,'ca':str(certs/'ca.pem')}
 private.write_text(json.dumps(data));private.chmod(0o600)
 public={k:v for k,v in data.items() if k!='password'};(work/('server-'+version+'.json')).write_text(json.dumps(public,indent=2)+'\n')
 print(version,actual,'ready',name,port,flush=True)
