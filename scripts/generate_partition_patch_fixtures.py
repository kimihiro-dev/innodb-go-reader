#!/usr/bin/env python3
"""Capture independent partition SQL, dictionaries and locked physical files."""
import argparse
import gzip
import hashlib
import json
import subprocess
import uuid
from pathlib import Path
from mysql57_schema import Client, collect, collect_indexes, ident, literal, save_json

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--container', required=True)
parser.add_argument('--out', required=True)
args = parser.parse_args()
out = Path(args.out)
if out.exists():
    parser.error('output must not exist')
out.mkdir(parents=True)
client = Client(args.container)
database = 'innodb_partition_' + uuid.uuid4().hex[:12]
manifest = dict(database=database, status='incomplete', cases=[])


class PhysicalQueries:
    def __init__(self, name):
        self.name = name

    def objects(self, expression, tail=''):
        return client.objects(expression, tail.replace('t.NAME=' + literal(database + '/' + table), 't.NAME=' + literal(self.name)))


def capture(table_name, phase='current', supported=True):
    global table
    table = table_name
    name = table + '_' + phase
    client.query('FLUSH TABLES ' + ident(table) + ' FOR EXPORT')
    try:
        where = 'WHERE TABLE_SCHEMA=' + literal(database) + ' AND TABLE_NAME=' + literal(table)
        parts = client.objects("JSON_OBJECT('name',PARTITION_NAME,'sub',SUBPARTITION_NAME,'ordinal',PARTITION_ORDINAL_POSITION,'method',PARTITION_METHOD,'expression',PARTITION_EXPRESSION)",
                               'FROM INFORMATION_SCHEMA.PARTITIONS ' + where + ' ORDER BY PARTITION_ORDINAL_POSITION,SUBPARTITION_ORDINAL_POSITION')
        columns = client.objects("JSON_OBJECT('name',COLUMN_NAME,'type',DATA_TYPE,'extra',EXTRA)", 'FROM INFORMATION_SCHEMA.COLUMNS ' + where + ' ORDER BY ORDINAL_POSITION')
        stored = [c for c in columns if 'VIRTUAL GENERATED' not in c['extra']]
        fields = []
        for c in stored:
            field, typ = ident(c['name']), c['type'].upper()
            if typ in ('BINARY', 'VARBINARY') or typ.endswith('BLOB'):
                field = 'HEX(' + field + ')'
            elif typ == 'DECIMAL':
                field = 'CAST(' + field + ' AS CHAR)'
            fields.append(field)
        expression = 'JSON_ARRAY(' + ','.join(fields) + ')'
        primary = client.query('SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.STATISTICS ' + where + " AND INDEX_NAME='PRIMARY' ORDER BY SEQ_IN_INDEX")
        order = ' ORDER BY ' + ','.join(map(ident, primary)) if primary else ''
        rows = client.objects(expression, 'FROM ' + ident(table) + order)
        prefix = database + '/' + table + '#'
        physical = client.objects("JSON_OBJECT('name',t.NAME,'table_id',t.TABLE_ID,'space',t.SPACE,'path',f.PATH)",
                                  'FROM INFORMATION_SCHEMA.' + dictionary + 'TABLES t JOIN INFORMATION_SCHEMA.' + dictionary + 'DATAFILES f ON f.SPACE=t.SPACE WHERE LEFT(t.NAME,CHAR_LENGTH(' + literal(prefix) + '))=' + literal(prefix) + ' ORDER BY t.TABLE_ID')
        definition = dict(version=1, mysql_version=env['version'], database=database, table=table,
                          partition_type={'HASH': 1, 'KEY': 3, 'RANGE': 7, 'LIST': 8}.get(parts[0]['method'], 0), expression=parts[0]['expression'], partitions=[])
        files, sources, expected = [], {}, {}
        for i, part in enumerate(parts):
            key = part['sub'] or part['name']
            suffix = '#P#' + part['name'] + ('#SP#' + part['sub'] if part['sub'] else '')
            matches = [p for p in physical if p['name'].casefold().endswith(suffix.casefold())]
            assert len(matches) == 1, (part, physical)
            p = matches[0]
            local_rows = client.objects(expression, 'FROM ' + ident(table) + ' PARTITION(' + ident(key) + ')' + order)
            expected[key] = local_rows
            source = p['path'] if p['path'].startswith('/') else env['datadir'] + p['path'].removeprefix('./')
            data = subprocess.run(['docker', 'exec', args.container, 'cat', source], check=True, capture_output=True).stdout
            file = name + '-' + str(i) + '.ibd.gz'
            (out / file).write_bytes(gzip.compress(data, mtime=0))
            if major == 8:
                raw = subprocess.run(['docker', 'exec', args.container, 'ibd2sdi', source], check=True, capture_output=True).stdout
                assert json.loads(raw)
                (out / (file + '.sdi.json.gz')).write_bytes(gzip.compress(raw, mtime=0))
            elif supported:
                adapter = PhysicalQueries(p['name'])
                schema, evidence = collect(adapter, database, table)
                indexes = collect_indexes(adapter, schema, evidence)
                definition['partitions'].append(dict(name=key, number=i, table_id=p['table_id'], schema=schema, indexes=indexes))
                sources[key] = evidence
            files.append(dict(partition=key, file=file, rows=len(local_rows), bytes=len(data), sha256=hashlib.sha256(data).hexdigest()))
        metadata = dict(environment=env, database=database, table=table, directory=parts, physical=physical, columns=columns, partitions=sources)
        if major == 5 and supported:
            save_json(out / (name + '.partitions.json'), definition)
        save_json(out / (name + '.source.json'), metadata)
        (out / (name + '.expected.json.gz')).write_bytes(gzip.compress(json.dumps(dict(rows=rows, partitions=expected), ensure_ascii=False).encode(), mtime=0))
        (out / (name + '.sql')).write_text('\n'.join(client.query('SHOW CREATE TABLE ' + ident(table))) + '\n')
        manifest['cases'].append(dict(name=name, table=table, supported=supported, files=files, rows=len(rows), unordered=not primary, virtual=len(columns) != len(stored)))
        print(name, len(files), len(rows), flush=True)
    finally:
        client.query('UNLOCK TABLES')


try:
    env = client.objects("JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm)")[0]
    assert env['page_size'] == 16384
    major, _, patch = map(int, env['version'].split('-')[0].split('.'))
    dictionary = 'INNODB_SYS_' if major == 5 else 'INNODB_'
    manifest['environment'] = env
    client.query('CREATE DATABASE ' + ident(database) + ' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin')
    client.query('USE ' + ident(database))
    methods = dict(ranges='RANGE(n)(PARTITION P0 VALUES LESS THAN(10),PARTITION p1 VALUES LESS THAN(20),PARTITION empty_p VALUES LESS THAN MAXVALUE)',
                   lists='LIST(n)(PARTITION P0 VALUES IN(0,1),PARTITION p1 VALUES IN(2,3),PARTITION empty_p VALUES IN(4))',
                   hashes='HASH(n) PARTITIONS 3', keys='KEY(n) PARTITIONS 3')
    for form in ('DYNAMIC', 'COMPACT'):
        for method, clause in methods.items():
            table = method + '_' + form.lower()
            ddl = 'id INT NOT NULL,n INT NOT NULL,v VARCHAR(40) COLLATE utf8mb4_bin,payload LONGTEXT,b LONGBLOB,d DECIMAL(24,8),j JSON,PRIMARY KEY(id,n),KEY s(n),KEY px(v(3),id)'
            client.query('CREATE TABLE ' + table + '(' + ddl + ') ROW_FORMAT=' + form + ' PARTITION BY ' + clause)
            values = []
            for i in range(24):
                n = i % 4 if method == 'lists' else i % 20
                values.append("(%d,%d,'abc%d',%s,%s,-12.34567890,JSON_OBJECT('n',18446744073709551615,'s','界'))" %
                              (i, n, i, "REPEAT('界😀',5000)" if i % 11 == 0 else ('NULL' if i % 7 == 0 else "'small'"), "REPEAT(X'00FF',20000)" if i % 11 == 0 else "X''"))
            client.query('INSERT INTO ' + table + ' VALUES' + ','.join(values))
            client.query('UPDATE ' + table + " SET payload=CONCAT(payload,'changed') WHERE id=0")
            client.query('DELETE FROM ' + table + ' WHERE id=23')
            capture(table)
        table = 'hidden_' + form.lower()
        client.query('CREATE TABLE ' + table + '(id INT,n INT NULL,payload LONGTEXT,KEY s(n)) ROW_FORMAT=' + form + ' PARTITION BY HASH(n) PARTITIONS 3')
        client.query('INSERT INTO ' + table + " VALUES(1,NULL,NULL),(2,2,'one'),(2,2,'one'),(3,1,REPEAT('界',12000))")
        capture(table)
        table = 'generated_' + form.lower()
        client.query('CREATE TABLE ' + table + '(id INT,n INT,s BIGINT AS(n*3) STORED,v BIGINT AS(n+1) VIRTUAL,PRIMARY KEY(id,n)) ROW_FORMAT=' + form + ' PARTITION BY HASH(n) PARTITIONS 3')
        client.query('INSERT INTO ' + table + '(id,n) VALUES(1,2),(2,3),(3,-5)')
        capture(table)
        table = 'autoinc_' + form.lower()
        client.query('CREATE TABLE ' + table + '(id INT NOT NULL AUTO_INCREMENT,n INT NOT NULL,v VARCHAR(40),PRIMARY KEY(id,n),KEY s(n)) ROW_FORMAT=' + form + ' PARTITION BY ' + methods['ranges'])
        client.query('INSERT INTO ' + table + "(n,v) VALUES(1,'a'),(11,'b'),(1,'c')")
        capture(table)
        if major == 8 and patch >= 23:
            table = 'invisible_' + form.lower()
            client.query('CREATE TABLE ' + table + '(id INT,n INT,a INT INVISIBLE,s BIGINT AS(a*3) STORED INVISIBLE,PRIMARY KEY(id,n)) ROW_FORMAT=' + form + ' PARTITION BY ' + methods['ranges'])
            client.query('INSERT INTO ' + table + '(id,n,a) VALUES(1,1,2),(2,11,NULL)')
            capture(table)
        if major == 8 and patch >= 12:
            table = 'instant_' + form.lower()
            client.query('CREATE TABLE ' + table + '(id INT,n INT,v VARCHAR(40),PRIMARY KEY(id,n)) ROW_FORMAT=' + form + ' PARTITION BY ' + methods['ranges'])
            client.query('INSERT INTO ' + table + " VALUES(1,1,'old'),(2,11,NULL)")
            client.query('ALTER TABLE ' + table + " ADD a INT DEFAULT 7,ADD txt VARCHAR(30) DEFAULT '界😀',ALGORITHM=INSTANT")
            client.query('INSERT INTO ' + table + '(id,n,v) VALUES(3,1,\'new\')')
            capture(table, 'add')
            client.query('ALTER TABLE ' + table + ' REBUILD PARTITION p1')
            capture(table, 'partial_rebuild')
            if patch >= 30:
                client.query('ALTER TABLE ' + table + ' ADD z INT DEFAULT 42 FIRST,DROP v,ALGORITHM=INSTANT')
                capture(table, 'drop')
    table = 'tree'
    client.query('CREATE TABLE tree(id INT,n INT,v VARCHAR(2000) NULL,PRIMARY KEY(id,n),KEY s(n)) PARTITION BY ' + methods['ranges'])
    for start in range(0, 600, 50):
        client.query('INSERT INTO tree VALUES' + ','.join("(%d,%d,REPEAT('x',1800))" % (i, i % 20) for i in range(start, start + 50)))
    capture(table)
    table = 'ranges_dynamic'
    client.query('ALTER TABLE ' + table + ' REBUILD PARTITION P0')
    capture(table, 'rebuild_first', supported=not (major == 8 and patch == 12))
    client.query('CREATE TABLE swap_rows LIKE ' + table)
    client.query('ALTER TABLE swap_rows REMOVE PARTITIONING')
    client.query("INSERT INTO swap_rows VALUES(1000,1,'swap','交换',X'01',0,JSON_OBJECT('x',1))")
    client.query('ALTER TABLE ' + table + ' EXCHANGE PARTITION P0 WITH TABLE swap_rows')
    capture(table, 'exchange', supported=not (major == 8 and patch < 28))
    client.query('CREATE TABLE sub_rows(id INT,n INT,payload LONGTEXT,PRIMARY KEY(id,n)) PARTITION BY RANGE(n) SUBPARTITION BY HASH(id) '
                 '(PARTITION p0 VALUES LESS THAN(10)(SUBPARTITION s0,SUBPARTITION s1),PARTITION p1 VALUES LESS THAN MAXVALUE(SUBPARTITION s2,SUBPARTITION s3))')
    client.query("INSERT INTO sub_rows VALUES(1,1,'sub')")
    capture('sub_rows', supported=False)
    manifest['status'] = 'captured'
finally:
    save_json(out / 'manifest.json', manifest)
    (out / 'generation.sql.gz').write_bytes(gzip.compress('\n'.join(client.log).encode(), mtime=0))
    client.close()
