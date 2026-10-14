#!/usr/bin/env python3
"""Capture exact MySQL 8.0 patch SQL and official SDI under FOR EXPORT locks.

Docker is a test-fixture dependency only. Never derives expectations from Go.
"""
import argparse
import gzip
import hashlib
import json
import subprocess
import uuid
from pathlib import Path
from mysql57_schema import Client, ident, literal, save_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output directory must not exist')
    out.mkdir(parents=True)
    client = Client(args.container)
    database = 'innodb_reader_80_' + uuid.uuid4().hex[:12]
    manifest = dict(database=database, cases=[], status='incomplete')
    try:
        env = client.objects("JSON_OBJECT('version',VERSION(),'page_size',@@innodb_page_size,"
                             "'checksum',@@innodb_checksum_algorithm,'datadir',@@datadir)")[0]
        version = int(env['version'].split('-')[0].split('.')[-1])
        assert version in (11, 12, 16, 19, 23, 28, 30) and env['page_size'] == 16384
        manifest['environment'] = env
        client.query('CREATE DATABASE ' + ident(database) + ' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin')
        client.query('USE ' + ident(database))

        def capture(table, phase='current', secondary=None):
            name = table + '_' + phase
            client.query('FLUSH TABLES ' + ident(table) + ' FOR EXPORT')
            try:
                where = ' WHERE TABLE_SCHEMA=' + literal(database) + ' AND TABLE_NAME=' + literal(table)
                columns = client.objects("JSON_OBJECT('name',COLUMN_NAME,'type',DATA_TYPE,'extra',EXTRA)",
                                         'FROM INFORMATION_SCHEMA.COLUMNS' + where + ' ORDER BY ORDINAL_POSITION')
                stored = [c for c in columns if 'VIRTUAL GENERATED' not in c['extra']]

                def encoded(c, expression=None):
                    field, typ = expression or ident(c['name']), c['type'].upper()
                    if typ in ('BINARY', 'VARBINARY') or typ.endswith('BLOB'):
                        return 'HEX(' + field + ')'
                    if typ in ('DECIMAL', 'DATE', 'TIME', 'DATETIME', 'TIMESTAMP'):
                        return 'CAST(' + field + ' AS CHAR)'
                    if typ in ('BIT', 'YEAR'):
                        return 'CAST(' + field + ' AS UNSIGNED)'
                    if typ in ('GEOMETRY', 'POINT'):
                        return 'IF(' + field + " IS NULL,NULL,JSON_OBJECT('srid',ST_SRID(" + field + "),'wkb_hex',HEX(ST_AsBinary(" + field + ",'axis-order=long-lat'))))"
                    return field

                keys = client.query('SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.STATISTICS' + where + " AND INDEX_NAME='PRIMARY' ORDER BY SEQ_IN_INDEX")
                tail = ' FROM ' + ident(table)
                order = ' ORDER BY ' + ','.join(map(ident, keys)) if keys else ''
                expression = 'JSON_ARRAY(' + ','.join(encoded(c) for c in stored) + ')'
                rows = client.objects(expression, tail + order)
                expectations = []
                for index, fields in (secondary or {}).items():
                    # Explicit physical order, independently declared in this generator.
                    sql_order = ' ORDER BY ' + ','.join(f['expr'] for f in fields)
                    entries = client.objects('JSON_ARRAY(' + ','.join(encoded(next(c for c in stored if c['name'] == f['column']), f['expr'].removesuffix(' DESC')) for f in fields) + ')', tail + sql_order)
                    expectations.append(dict(index=index, entries=entries, rows=client.objects(expression, tail + sql_order)))
                indexes = client.objects("JSON_OBJECT('name',i.NAME,'index_id',i.INDEX_ID,'space_id',i.SPACE,'root_page',i.PAGE_NO)",
                                         'FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID WHERE t.NAME=' + literal(database + '/' + table))
                source = env['datadir'] + database + '/' + table + '.ibd'
                data = subprocess.run(['docker', 'exec', args.container, 'cat', source], check=True, capture_output=True).stdout
                sdi = subprocess.run(['docker', 'exec', args.container, 'ibd2sdi', source], check=True, capture_output=True).stdout
                assert json.loads(sdi)
                ddl = '\n'.join(client.query('SHOW CREATE TABLE ' + ident(table))).split('\t', 1)[1]
            finally:
                client.query('UNLOCK TABLES')
            for suffix, data_out in [('ibd', data), ('expected.json', json.dumps(rows, ensure_ascii=False).encode()),
                                     ('sdi.json', sdi), ('secondary.json', json.dumps(expectations, ensure_ascii=False).encode())]:
                (out / (name + '.' + suffix + '.gz')).write_bytes(gzip.compress(data_out, mtime=0))
            save_json(out / (name + '.source.json'), dict(environment=env, columns=columns, indexes=indexes, table=table, database=database))
            (out / (name + '.sql')).write_text(ddl + ';\n')
            manifest['cases'].append(dict(name=name, rows=len(rows), sha256=hashlib.sha256(data).hexdigest(), bytes=len(data), unordered=not keys, virtual=len(columns) != len(stored)))
            print(name, len(rows), len(data), flush=True)

        def create(name, ddl, form):
            client.query('CREATE TABLE ' + ident(name) + '(' + ddl + ') ROW_FORMAT=' + form)

        for form in ('DYNAMIC', 'COMPACT'):
            tag = form.lower()
            name = 'scalar_' + tag
            create(name, "id INT PRIMARY KEY,ti TINYINT,sm SMALLINT,med MEDIUMINT,si INT,bi BIGINT,bu BIGINT UNSIGNED,"
                         "d DECIMAL(65,30),f FLOAT,x DOUBLE,bits BIT(64),y YEAR,dt DATE,tm TIME(6),ts TIMESTAMP(6) NULL,dd DATETIME(6),"
                         "ch CHAR(8),v VARCHAR(300),b BINARY(5),vb VARBINARY(300),e ENUM('','界','a,b'),s SET('界','a'),j JSON,g POINT", form)
            values = ["1,-128,-32768,-8388608,-2147483648,-9223372036854775808,18446744073709551615,"
                      "12345678901234567890123456789012345.123456789012345678901234567890,1.5,-2.25,18446744073709551615,2155,"
                      "'2024-02-29','-838:59:58.123456','2024-02-29 12:34:56.654321','2024-02-29 23:59:59.123456',"
                      "'界😀  ',REPEAT('界',100),X'00FF41',X'00FF4142','a,b','界,a',JSON_OBJECT('n',18446744073709551615,'a',JSON_ARRAY(NULL,TRUE,'界')),ST_GeomFromText('POINT(1.5 -2.25)',4326)",
                      '2,' + ','.join(['NULL'] * 23)]
            client.query('INSERT INTO ' + name + ' VALUES(' + '),('.join(values) + ')')
            capture(name)
            name = 'lob_' + tag
            create(name, 'id INT PRIMARY KEY,t LONGTEXT,b LONGBLOB,j JSON', form)
            client.query('INSERT INTO ' + name + " VALUES(1,REPEAT('界😀',12000),REPEAT(X'00FF4142',24000),JSON_OBJECT('s',REPEAT('a',40000))),"
                         "(2,'',X'',JSON_ARRAY()),(3,NULL,NULL,NULL)")
            capture(name, 'insert')
            client.query('UPDATE ' + name + " SET t=INSERT(t,20,1,'改'),b=INSERT(b,50,1,X'01'),j=JSON_SET(j,'$.s',CONCAT('b',SUBSTRING(j->>'$.s',2))) WHERE id=1")
            capture(name, 'small_update')
            client.query('UPDATE ' + name + " SET t=REPEAT('新',16000),b=REPEAT(X'FF',100000),j=JSON_OBJECT('z',REPEAT('c',70000)) WHERE id=1")
            client.query('DELETE FROM ' + name + ' WHERE id=2')
            capture(name, 'replace_delete')
            name = 'secondary_' + tag
            create(name, 'id INT PRIMARY KEY,n INT NULL,v VARCHAR(40) COLLATE utf8mb4_bin,payload LONGTEXT,KEY s(n DESC),KEY px(v(3),n)', form)
            client.query('INSERT INTO ' + name + " VALUES(1,NULL,NULL,'null'),(2,-1,'abc-one',REPEAT('界',12000)),(3,-1,'abc-two','two'),"
                         "(4,2,'界😀a-one',''),(5,2,'界😀a-two',NULL),(6,2,'','empty')")
            capture(name, secondary=dict(s=[dict(column='n', expr='n DESC'), dict(column='id', expr='id')],
                                         px=[dict(column='v', expr='LEFT(v,3)'), dict(column='n', expr='n'), dict(column='id', expr='id')]))
            name = 'hidden_' + tag
            create(name, 'seq INT NULL,v VARCHAR(30),payload LONGTEXT', form)
            client.query('INSERT INTO ' + name + " VALUES(4,'abc','one'),(4,'abd','two'),(NULL,NULL,NULL),(4,'abc',REPEAT('界',12000))")
            capture(name)
            name = 'generated_' + tag
            create(name, 'id INT PRIMARY KEY,n INT,s BIGINT AS(n*3) STORED,v BIGINT AS(n+1) VIRTUAL', form)
            client.query('INSERT INTO ' + name + '(id,n) VALUES(1,2),(2,NULL),(3,-5)')
            capture(name)
            name = 'tree_' + tag
            create(name, 'id INT PRIMARY KEY,v VARCHAR(2000)', form)
            for start in range(0, 500, 50):
                client.query('INSERT INTO ' + name + ' VALUES' + ','.join("(%d,REPEAT('x',1800))" % i for i in range(start, start + 50)))
            capture(name)
            if version >= 12:
                name = 'instant_' + tag
                create(name, 'id INT PRIMARY KEY,v VARCHAR(30) NULL', form)
                client.query('INSERT INTO ' + name + " VALUES(1,'old'),(2,NULL)")
                client.query('ALTER TABLE ' + name + " ADD a INT NOT NULL DEFAULT 7,ADD b VARCHAR(30) NULL,ALGORITHM=INSTANT")
                client.query('INSERT INTO ' + name + " VALUES(3,'middle',9,'mid')")
                capture(name, 'add')
                client.query('ALTER TABLE ' + name + " ADD c VARCHAR(20) NOT NULL DEFAULT '界😀',ADD d INT NULL DEFAULT 42,ALGORITHM=INSTANT")
                client.query('ALTER TABLE ' + name + ' ALTER a SET DEFAULT 99')
                client.query('INSERT INTO ' + name + "(id,v,b) VALUES(4,'new','last')")
                client.query('UPDATE ' + name + " SET b='updated' WHERE id=1")
                capture(name, 'mixed')
                client.query('ALTER TABLE ' + name + ' FORCE,ALGORITHM=INPLACE')
                capture(name, 'rebuilt')
                name = 'wide_' + tag
                create(name, 'id INT PRIMARY KEY,' + ','.join('n%d INT NULL' % i for i in range(126)), form)
                client.query('INSERT INTO ' + name + '(id) VALUES(1)')
                client.query('ALTER TABLE ' + name + ' ADD a INT NOT NULL DEFAULT 7,ALGORITHM=INSTANT')
                client.query('INSERT INTO ' + name + '(id) VALUES(2)')
                capture(name)
                name = 'instant_hidden_' + tag
                create(name, 'n INT NULL', form)
                client.query('INSERT INTO ' + name + ' VALUES(1),(NULL)')
                client.query('ALTER TABLE ' + name + ' ADD a INT DEFAULT 7,ALGORITHM=INSTANT')
                capture(name)
                name = 'instant_virtual_' + tag
                create(name, 'id INT PRIMARY KEY,v INT AS(id+1) VIRTUAL,n INT NULL', form)
                client.query('INSERT INTO ' + name + '(id,n) VALUES(1,NULL),(2,3)')
                client.query('ALTER TABLE ' + name + ' ADD a INT DEFAULT 7,ALGORITHM=INSTANT')
                capture(name)
                name = 'instant_defaults_' + tag
                create(name, 'id INT PRIMARY KEY', form)
                client.query('INSERT INTO ' + name + ' VALUES(1)')
                client.query('ALTER TABLE ' + name + " ADD d DECIMAL(10,3) NOT NULL DEFAULT -12.345,ADD v VARBINARY(20) NOT NULL DEFAULT X'00FF',"
                             "ADD e ENUM('','界','abc') NOT NULL DEFAULT '界',ADD t DATETIME(6) NOT NULL DEFAULT '2024-02-29 12:34:56.123456',"
                             "ADD z VARCHAR(20) NOT NULL DEFAULT '',ALGORITHM=INSTANT")
                client.query('INSERT INTO ' + name + '(id) VALUES(2)')
                capture(name)
                name = 'instant_tree_' + tag
                create(name, 'id INT PRIMARY KEY,v VARCHAR(2000) NULL', form)
                for start in range(0, 500, 50):
                    client.query('INSERT INTO ' + name + ' VALUES' + ','.join("(%d,REPEAT('x',1800))" % i for i in range(start, start + 50)))
                client.query('ALTER TABLE ' + name + ' ADD a INT NULL DEFAULT 7,ADD b INT NULL,ALGORITHM=INSTANT')
                client.query('INSERT INTO ' + name + " VALUES(501,'new',9,NULL)")
                client.query('UPDATE ' + name + ' SET a=8 WHERE id=1')
                capture(name)
            if version >= 23:
                name = 'invisible_' + tag
                create(name, 'id INT PRIMARY KEY,n INT INVISIBLE,s BIGINT AS(n*3) STORED INVISIBLE', form)
                client.query('INSERT INTO ' + name + '(id,n) VALUES(1,2),(2,NULL)')
                capture(name)
            if version >= 30:
                name = 'drop_' + tag
                create(name, 'id INT PRIMARY KEY,n INT,v VARCHAR(30)', form)
                client.query('INSERT INTO ' + name + " VALUES(1,2,'old')")
                client.query('ALTER TABLE ' + name + ' ADD a INT DEFAULT 7 FIRST,DROP n,ALGORITHM=INSTANT')
                client.query('INSERT INTO ' + name + " VALUES(9,2,'new')")
                capture(name)
        if version >= 30:
            client.query('SET SESSION sql_generate_invisible_primary_key=ON')
            create('gipk', 'n INT,v VARCHAR(30),KEY s(n)', 'DYNAMIC')
            client.query("INSERT INTO gipk(n,v) VALUES(2,'a'),(NULL,'b'),(2,'c')")
            capture('gipk')
        manifest['status'] = 'captured'
    finally:
        save_json(out / 'manifest.json', manifest)
        (out / 'generation.sql').write_text('\n'.join(client.log) + '\n')
        client.close()


if __name__ == '__main__':
    main()
