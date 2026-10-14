import pathlib,json,subprocess
w=pathlib.Path('/tmp/innodb-57-patch-run');report={}
for v in ['5.7.9','5.7.10','5.7.11','5.7.12','5.7.28','5.7.43']:
 c=json.loads((w/('private-'+v+'.json')).read_text())
 p=subprocess.run(['docker','exec',c['container'],'mysql','--no-defaults','-uroot','--protocol=tcp','-h127.0.0.1','--ssl','--batch','--skip-column-names','-e',"SHOW VARIABLES LIKE 'have_openssl';SHOW VARIABLES LIKE 'tls_version';SHOW STATUS LIKE 'Ssl_version';SHOW STATUS LIKE 'Ssl_cipher'"],capture_output=True,text=True,timeout=30)
 report[v]={'exit_code':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
 print(v,p.stdout.strip().replace('\n','; '),flush=True)
(w/'tls-capabilities.json').write_text(json.dumps(report,indent=2)+'\n')
