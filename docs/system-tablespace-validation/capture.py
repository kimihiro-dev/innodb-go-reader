import gzip, hashlib, json, shutil, subprocess, tempfile, time
from pathlib import Path

ROOT = Path.cwd()
OUT = ROOT / 'docs/system-tablespace-validation'
OUT.mkdir(exist_ok=False)
TEMP = Path(tempfile.mkdtemp(prefix='innodb-systemspace-docs-'))
BASE80 = Path('/Users/kimihiro/workspace/software/mysql-8.0.45')
IMAGE57 = 'mysql@sha256:dab0a802b44617303694fb17d166501de279c3031ddeb28c56ecf7fcab5ef0da'
NAME57 = 'innodb-systemspace-57-doc-20261013-b'
logs = []


def run(command, check=True):
    result = subprocess.run(list(map(str, command)), capture_output=True, text=True)
    logs.append(dict(command=list(map(str, command)), exit=result.returncode, stdout=result.stdout, stderr=result.stderr))
    if check and result.returncode:
        raise RuntimeError(str(command) + ': ' + result.stderr)
    return result


def capture(version, prefix, copyfile, stop):
    folder = OUT / version
    folder.mkdir()
    statements = []
    def sql(statement):
        statements.append(statement)
        return run(prefix + ['-e', statement]).stdout.strip()
    def obj(expression, tail=''):
        return [json.loads(bytes.fromhex(row)) for row in sql('SELECT HEX(CAST(' + expression + ' AS CHAR CHARACTER SET utf8mb4)) ' + tail).splitlines()]
    for attempt in range(120):
        if run(prefix + ['-e', 'SELECT 1'], check=False).returncode == 0:
            break
        time.sleep(.25)
    else:
        raise RuntimeError('server readiness timeout')
    sql("CREATE DATABASE lesson CHARACTER SET utf8mb4 COLLATE utf8mb4_bin")
    sql('SET GLOBAL innodb_file_per_table=OFF')
    sql('CREATE TABLE lesson.shared_rows(id INT PRIMARY KEY,n INT NULL,KEY n_idx(n)) ENGINE=InnoDB ROW_FORMAT=COMPACT')
    sql('INSERT INTO lesson.shared_rows VALUES(1,10),(2,NULL)')
    sql('SET GLOBAL innodb_file_per_table=ON')
    sql('CREATE TABLE lesson.private_rows(id INT PRIMARY KEY,n INT NULL,marker VARCHAR(20) COLLATE utf8mb4_bin,KEY n_idx(n)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
    sql("INSERT INTO lesson.private_rows VALUES(1,10,'one'),(2,NULL,'two')")
    env = obj("JSON_OBJECT('version',VERSION(),'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table,'undo_tablespaces',@@innodb_undo_tablespaces,'doublewrite',@@innodb_doublewrite,'data_file_path',@@innodb_data_file_path,'datadir',@@datadir,'change_buffering',@@innodb_change_buffering)")[0]
    assert env['version'].split('-',1)[0] == version and env['page_size'] == 16384
    old = version.startswith('5.')
    itable = 'INNODB_SYS_TABLES' if old else 'INNODB_TABLES'
    iindex = 'INNODB_SYS_INDEXES' if old else 'INNODB_INDEXES'
    tables = obj("JSON_OBJECT('name',NAME,'table_id',TABLE_ID,'space',SPACE,'flags',FLAG,'row_format',ROW_FORMAT,'columns',N_COLS)",
                 'FROM INFORMATION_SCHEMA.' + itable + " WHERE NAME LIKE 'lesson/%' ORDER BY NAME")
    indexes = obj("JSON_OBJECT('table',t.NAME,'name',i.NAME,'id',i.INDEX_ID,'root',i.PAGE_NO,'space',i.SPACE,'type',i.TYPE,'fields',i.N_FIELDS)",
                  'FROM INFORMATION_SCHEMA.' + iindex + ' i JOIN INFORMATION_SCHEMA.' + itable + " t ON i.TABLE_ID=t.TABLE_ID WHERE t.NAME LIKE 'lesson/%' ORDER BY t.NAME,i.INDEX_ID")
    rows = {name: obj('JSON_ARRAY(id,n' + (',marker' if name == 'private_rows' else '') + ')', 'FROM lesson.' + name + ' ORDER BY id') for name in ('shared_rows','private_rows')}
    dictionary = obj("JSON_OBJECT('name',NAME,'id',INDEX_ID,'root',PAGE_NO,'space',SPACE)", 'FROM INFORMATION_SCHEMA.INNODB_SYS_INDEXES WHERE TABLE_ID IN (1,2,3,4) ORDER BY TABLE_ID,INDEX_ID') if old else []
    sql('SET GLOBAL innodb_fast_shutdown=0')
    stop()
    files = ['ibdata1','lesson/private_rows.ibd']
    if not old:
        files += ['mysql.ibd','undo_001','undo_002']
    artifacts = []
    for relative in files:
        path = TEMP / version / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        copyfile(relative,path)
        data = path.read_bytes()
        name = relative.replace('/', '_') + '.gz'
        (folder/name).write_bytes(gzip.compress(data,mtime=0))
        artifacts.append(dict(source=relative,artifact=name,bytes=len(data),sha256=hashlib.sha256(data).hexdigest()))
    (folder/'source.json').write_text(json.dumps(dict(environment=env,tables=tables,indexes=indexes,dictionary_roots_sql=dictionary,rows=rows,
                                                     capture='dedicated instance; innodb_fast_shutdown=0; clean shutdown before copying',files=artifacts),ensure_ascii=False,indent=2)+'\n')
    (folder/'session.sql').write_text(';\n'.join(statements)+';\n')
    print(version,'captured',len(files),'files',flush=True)


try:
    run(['docker','run','--detach','--name',NAME57,'--platform','linux/amd64','--network','none',
         '--mount','type=volume,source='+NAME57+',target=/var/lib/mysql','--env','MYSQL_ALLOW_EMPTY_PASSWORD=yes',IMAGE57,
         '--innodb-page-size=16384','--innodb-checksum-algorithm=strict_crc32','--skip-log-bin'])
    prefix57=['docker','exec',NAME57,'mysql','--no-defaults','-uroot','--batch','--raw','--skip-column-names','--default-character-set=utf8mb4']
    def stop57():
        run(['docker','exec',NAME57,'mysqladmin','--no-defaults','-uroot','shutdown'],check=False)
        for _ in range(120):
            if run(['docker','inspect','--format','{{.State.Running}}',NAME57]).stdout.strip() == 'false':
                break
            time.sleep(.25)
        assert run(['docker','inspect','--format','{{.State.ExitCode}}',NAME57]).stdout.strip() == '0'
        result=run(['docker','logs',NAME57]);shutdown=result.stdout+result.stderr;assert 'Shutdown complete' in shutdown
        (OUT/'mysql57-shutdown.log').write_text(shutdown)
    capture('5.7.44',prefix57,lambda src,dst:run(['docker','cp',NAME57+':/var/lib/mysql/'+src,dst]),stop57)
    data80=TEMP/'mysql80'/'data';data80.mkdir(parents=True)
    error80=TEMP/'mysql80'/'error.log';socket80=TEMP/'mysql80'/'mysql.sock'
    run([BASE80/'bin/mysqld','--no-defaults','--initialize-insecure','--basedir='+str(BASE80),'--datadir='+str(data80),'--log-error='+str(error80)])
    run([BASE80/'bin/mysqld','--no-defaults','--daemonize','--basedir='+str(BASE80),'--datadir='+str(data80),'--socket='+str(socket80),
         '--pid-file='+str(TEMP/'mysql80'/'mysql.pid'),'--log-error='+str(error80),'--skip-networking','--mysqlx=OFF','--skip-log-bin',
         '--innodb-page-size=16384','--innodb-checksum-algorithm=strict_crc32','--innodb-buffer-pool-size=64M','--innodb-buffer-pool-instances=1'])
    prefix80=[str(BASE80/'bin/mysql'),'--no-defaults','-uroot','--socket='+str(socket80),'--batch','--raw','--skip-column-names','--default-character-set=utf8mb4']
    def stop80():
        run([BASE80/'bin/mysqladmin','--no-defaults','-uroot','--socket='+str(socket80),'shutdown'])
        for _ in range(120):
            if not socket80.exists():
                break
            time.sleep(.25)
        log=error80.read_text();assert 'Shutdown complete' in log
        (OUT/'mysql80-shutdown.log').write_text(log)
    capture('8.0.45',prefix80,lambda src,dst:shutil.copyfile(data80/src,dst),stop80)
    inventory=[]
    for p in sorted(data80.rglob('*')):
        if p.is_file() and (p.name.startswith(('#ib_','ibdata','undo_','mysql.ibd')) or '#innodb_redo' in p.parts):
            inventory.append(dict(path=str(p.relative_to(data80)),bytes=p.stat().st_size))
    (OUT/'mysql80-files.json').write_text(json.dumps(inventory,indent=2)+'\n')
finally:
    (OUT/'capture-log.json').write_text(json.dumps(logs,ensure_ascii=False,indent=2)+'\n')
    (OUT/'host.json').write_text(json.dumps(dict(temporary_root=str(TEMP),mysql80_basedir=str(BASE80),mysql57_container=NAME57,mysql57_image=IMAGE57),indent=2)+'\n')
