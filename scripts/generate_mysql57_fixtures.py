#!/usr/bin/env python3
"""Generate independent MySQL 5.7.44 evidence in a new dedicated database."""
import argparse
import gzip
import hashlib
import json
import uuid
from datetime import datetime, timezone
from pathlib import Path
from mysql57_schema import Client, collect, ident, save_json, snapshot


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output directory must not exist')
    out.mkdir(parents=True)
    database = 'innodb_reader_57_' + uuid.uuid4().hex[:12]
    client = Client(args.container)
    manifest = dict(created=datetime.now(timezone.utc).isoformat(), database=database, cases=[], status='incomplete')
    try:
        client.query('CREATE DATABASE ' + ident(database) + ' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin')
        client.query('USE ' + ident(database))

        def capture(table, phase):
            name = table + '_' + phase
            client.query('FLUSH TABLES ' + ident(table) + ' FOR EXPORT')
            try:
                schema, evidence = collect(client, database, table)
                expressions = []
                geometry = {'GEOMETRY', 'POINT', 'LINESTRING', 'POLYGON', 'MULTIPOINT', 'MULTILINESTRING', 'MULTIPOLYGON', 'GEOMETRYCOLLECTION'}
                for col in schema['columns']:
                    field, typ = ident(col['name']), col['type']
                    if typ in ('BINARY', 'VARBINARY') or typ.endswith('BLOB'):
                        field = 'HEX(' + field + ')'
                    elif typ in ('DECIMAL', 'DATE', 'TIME', 'DATETIME', 'TIMESTAMP'):
                        field = 'CAST(' + field + ' AS CHAR)'
                    elif typ in ('BIT', 'YEAR'):
                        field = 'CAST(' + field + ' AS UNSIGNED)'
                    elif typ in geometry:
                        field = 'IF(' + field + " IS NULL,NULL,JSON_OBJECT('srid',ST_SRID(" + field + "),'wkb_hex',HEX(ST_AsBinary(" + field + '))))'
                    expressions.append(field)
                keys = schema.get('primary_keys') or schema.get('clustered_key', {}).get('columns', [])
                tail = 'FROM ' + ident(table)
                if keys:
                    tail += ' ORDER BY ' + ','.join(map(ident, keys))
                rows = client.objects('JSON_ARRAY(' + ','.join(expressions) + ')', tail)
                data = snapshot(client, evidence)
                ddl = '\n'.join(client.query('SHOW CREATE TABLE ' + ident(table))).split('\t', 1)[1]
            finally:
                client.query('UNLOCK TABLES')
            (out / (name + '.ibd.gz')).write_bytes(gzip.compress(data, mtime=0))
            (out / (name + '.expected.json.gz')).write_bytes(gzip.compress((json.dumps(rows, ensure_ascii=False) + '\n').encode(), mtime=0))
            save_json(out / (name + '.json'), schema)
            save_json(out / (name + '.source.json'), evidence)
            (out / (name + '.sql')).write_text(ddl + ';\n')
            manifest['cases'].append(dict(name=name, rows=len(rows), sha256=hashlib.sha256(data).hexdigest(),
                                          bytes=len(data), unordered=not keys, format=schema['row_format'],
                                          virtual=bool(schema.get('virtual_columns'))))
            print(name, 'rows', len(rows), 'bytes', len(data), flush=True)

        def create(name, definition, form):
            client.query('CREATE TABLE ' + ident(name) + '(' + definition + ') ROW_FORMAT=' + form)

        for form in ('DYNAMIC', 'COMPACT'):
            tag = form.lower()
            name = 'scalar_' + tag
            declarations = dict(id='INT PRIMARY KEY', ti='TINYINT', tu='TINYINT UNSIGNED', sm='SMALLINT', su='SMALLINT UNSIGNED',
                                med='MEDIUMINT', mu='MEDIUMINT UNSIGNED', si='INT', iu='INT UNSIGNED', bi='BIGINT', bu='BIGINT UNSIGNED',
                                decv='DECIMAL(65,30)', fv='FLOAT', dv='DOUBLE', bv='BIT(64)', y='YEAR', d='DATE',
                                dt='DATETIME(6)', tm='TIME(6)', ts='TIMESTAMP(6) NULL', ch='CHAR(8)', v='VARCHAR(300)',
                                b='BINARY(5)', vb='VARBINARY(300)', e="ENUM('','界','O''Brien','a,b','back\\\\slash')",
                                s="SET('界','O''Brien','back\\\\slash')", j='JSON')
            wkts = dict(g='GEOMETRYCOLLECTION(POINT(1 2),LINESTRING(0 0,3 4))', p='POINT(1.5 -2.25)',
                        line='LINESTRING(0 0,1 2)', poly='POLYGON((0 0,2 0,2 2,0 0))', mp='MULTIPOINT(1 2,3 4)',
                        ml='MULTILINESTRING((0 0,1 1),(2 2,3 3))', mpoly='MULTIPOLYGON(((0 0,2 0,2 2,0 0)))')
            declarations.update(g='GEOMETRY', p='POINT', line='LINESTRING', poly='POLYGON', mp='MULTIPOINT', ml='MULTILINESTRING', mpoly='MULTIPOLYGON')
            create(name, ','.join(ident(k) + ' ' + v for k, v in declarations.items()), form)
            first = dict(id='1', ti='-128', tu='255', sm='-32768', su='65535', med='-8388608', mu='16777215',
                         si='-2147483648', iu='4294967295', bi='-9223372036854775808', bu='18446744073709551615',
                         decv='12345678901234567890123456789012345.123456789012345678901234567890', fv='1.5', dv='-2.25',
                         bv='18446744073709551615', y='2155', d="'2024-02-29'", dt="'2024-02-29 23:59:59.123456'",
                         tm="'-838:59:58.123456'", ts="'2024-02-29 12:34:56.654321'", ch="'界😀  '", v="REPEAT('界',100)",
                         b="X'00FF41'", vb="X'00FF4142'", e="'O''Brien'", s="'界,O''Brien'", j="JSON_OBJECT('n',18446744073709551615,'array',JSON_ARRAY(NULL,TRUE,'界'))")
            first.update({k: "ST_GeomFromText('" + v + "',4326)" for k, v in wkts.items()})
            third = dict(id='3', decv='0', bv='0', y='0', d="'0000-00-00'", dt="'0000-00-00 00:00:00'",
                         tm="'00:00:00'", ts="'0000-00-00 00:00:00'", ch="''", v="''", b="X''", vb="X''", e="''", s="''", j="CAST('null' AS JSON)")
            for row in (first, dict(id='2'), third):
                client.query('INSERT INTO ' + ident(name) + ' VALUES(' + ','.join(row.get(c, 'NULL') for c in declarations) + ')')
            capture(name, 'initial')
            name = 'lob_' + tag
            create(name, 'id INT PRIMARY KEY,txt LONGTEXT,bin LONGBLOB,v VARCHAR(8000),vb VARBINARY(20000),j JSON', form)
            client.query('INSERT INTO ' + name + " VALUES(1,REPEAT('界😀',10000),REPEAT(X'00FF0102',17000),REPEAT('😀',6000),REPEAT(X'AA00',9000),JSON_OBJECT('s',REPEAT('界',24000))),(2,NULL,NULL,NULL,NULL,NULL),(3,'',X'','',X'',JSON_OBJECT())")
            capture(name, 'initial')
            client.query('UPDATE ' + name + " SET txt=REPEAT('更新😀',12000),bin=REPEAT(X'AB00',50000),j=JSON_SET(j,'$.s',REPEAT('更',26000)) WHERE id=1")
            capture(name, 'grown')
            client.query('UPDATE ' + name + " SET txt='short',bin=X'00',v='',vb=X'',j=JSON_OBJECT('n',1) WHERE id=1")
            client.query('DELETE FROM ' + name + ' WHERE id=2')
            capture(name, 'shrunk_deleted')
            name = 'families_' + tag
            create(name, 'id INT PRIMARY KEY,tt TINYTEXT,t TEXT,mt MEDIUMTEXT,lt LONGTEXT,tb TINYBLOB,b BLOB,mb MEDIUMBLOB,lb LONGBLOB', form)
            client.query('INSERT INTO ' + name + " VALUES(1,REPEAT('界',80),REPEAT('界',18000),REPEAT('界',30000),REPEAT('界',40000),REPEAT(X'FF',255),REPEAT(X'FF',60000),REPEAT(X'FF',80000),REPEAT(X'FF',100000))")
            capture(name, 'initial')
            name = 'tree_' + tag
            create(name, 'k VARBINARY(767) NOT NULL,seq INT NOT NULL,padding VARCHAR(1400) NOT NULL,PRIMARY KEY(k DESC,seq)', form)
            for start in range(0, 240, 40):
                values = ["(CONCAT(REPEAT(X'FF',766),UNHEX(LPAD(HEX(" + str(i % 7) + "),2,'0')))," + str(i) + ",REPEAT('t',1200))" for i in range(start, start + 40)]
                client.query('INSERT INTO ' + name + ' VALUES' + ','.join(values))
            capture(name, 'initial')
            name = 'hidden_' + tag
            create(name, 'seq INT,payload LONGTEXT', form)
            client.query('INSERT INTO ' + name + " VALUES(4,REPEAT('界😀',9000)),(4,'duplicate'),(NULL,NULL)")
            capture(name, 'initial')
            name = 'unique_' + tag
            create(name, 'seq INT,k INT NOT NULL,payload VARCHAR(20),UNIQUE KEY actual_unique(k)', form)
            client.query('INSERT INTO ' + name + " VALUES(1,9,'nine'),(2,-1,'minus'),(3,3,NULL)")
            capture(name, 'initial')
            name = 'generated_' + tag
            create(name, 'id INT PRIMARY KEY,base INT,v INT AS(base+100) VIRTUAL,s INT AS(base+1) STORED', form)
            client.query('INSERT INTO ' + name + '(id,base) VALUES(1,7),(2,NULL)')
            capture(name, 'initial')
            name = 'charset_' + tag
            create(name, 'id INT PRIMARY KEY,a CHAR(5) CHARACTER SET ascii,l VARCHAR(20) CHARACTER SET latin1,u VARCHAR(20) CHARACTER SET utf8', form)
            client.query('INSERT INTO ' + name + " VALUES(1,'a  ',CONVERT(X'8081FF' USING latin1),'界'),(2,'','',''),(3,NULL,NULL,NULL)")
            capture(name, 'initial')
            name = 'empty_' + tag
            create(name, 'id INT PRIMARY KEY,payload VARCHAR(40)', form)
            capture(name, 'initial')
        manifest['status'] = 'captured'
    finally:
        (out / 'writer.sql.gz').write_bytes(gzip.compress(('\n'.join(client.log) + '\n').encode(), mtime=0))
        save_json(out / 'manifest.json', manifest)
        client.close()


if __name__ == '__main__':
    main()
