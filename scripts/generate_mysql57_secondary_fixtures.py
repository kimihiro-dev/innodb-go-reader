#!/usr/bin/env python3
"""Capture 5.7.44 secondary layouts and independent SQL query expectations."""
import argparse
import gzip
import hashlib
import json
import uuid
from datetime import datetime, timezone
from pathlib import Path
from mysql57_schema import Client, collect, collect_indexes, ident, literal, save_json, snapshot


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output directory must not exist')
    out.mkdir(parents=True)
    database = 'innodb_reader_57b_' + uuid.uuid4().hex[:12]
    client = Client(args.container)
    manifest = dict(created=datetime.now(timezone.utc).isoformat(), database=database, cases=[], status='incomplete')
    try:
        client.query('CREATE DATABASE ' + ident(database) + ' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin')
        client.query('USE ' + ident(database))

        def capture(name):
            client.query('FLUSH TABLES ' + ident(name) + ' FOR EXPORT')
            try:
                schema, evidence = collect(client, database, name)
                indexes = collect_indexes(client, schema, evidence)
                hidden = schema.get('clustered_key', {}).get('hidden_row_id', False)

                def expression(c, prefix=0):
                    value = ident(c['name'])
                    if prefix:
                        width = {'utf8mb4': 4, 'utf8': 3, 'ascii': 1, 'latin1': 1}.get(c.get('charset'), 1)
                        value = 'LEFT(' + value + ',' + str(prefix // width) + ')'
                    return value

                def encoded(c, value=None):
                    value = value or expression(c)
                    if c['type'] in ('BINARY', 'VARBINARY') or c['type'].endswith('BLOB'):
                        return 'HEX(' + value + ')'
                    if c['type'] in ('DECIMAL', 'DATE', 'TIME', 'DATETIME', 'TIMESTAMP'):
                        return 'CAST(' + value + ' AS CHAR)'
                    if c['type'] in ('BIT', 'YEAR'):
                        return 'CAST(' + value + ' AS UNSIGNED)'
                    return value

                columns = schema['columns']
                keys = schema.get('primary_keys') or schema.get('clustered_key', {}).get('columns', [])
                tail = 'FROM ' + ident(name)
                row_order = ' ORDER BY ' + ','.join(map(ident, keys)) if keys else ''
                rows = client.objects('JSON_ARRAY(' + ','.join(encoded(c) for c in columns) + ')', tail + row_order)
                expectations = []
                for index in indexes:
                    physical = [f for f in index['Fields'] if not f['RowID']]
                    order = [expression(f['Definition'], f['PrefixBytes']) for f in physical]
                    entries = client.objects('JSON_ARRAY(' + ','.join(encoded(f['Definition'], expression(f['Definition'], f['PrefixBytes'])) for f in physical) + ')',
                                             tail + ' ORDER BY ' + ','.join(order))
                    coverage = list(dict.fromkeys(f['Definition']['name'] for f in physical if not f['PrefixBytes']))
                    scenarios = [dict(label='all', columns=None)]
                    if coverage:
                        scenarios.append(dict(label='covered', columns=coverage))
                    if rows:
                        declared = index['Fields'][:index['UserFields']]
                        first = client.objects('JSON_ARRAY(' + ','.join(encoded(f['Definition']) for f in declared) + ')',
                                               tail + ' ORDER BY ' + ','.join(order) + ' LIMIT 1')[0]
                        last = client.objects('JSON_ARRAY(' + ','.join(encoded(f['Definition']) for f in declared) + ')',
                                              tail + ' ORDER BY ' + ','.join(expression(f['Definition']) + ' DESC' for f in declared) + ' LIMIT 1')[0]
                        low = client.objects('JSON_ARRAY(' + ','.join(encoded(f['Definition']) for f in declared) + ')',
                                             tail + ' ORDER BY ' + ','.join(expression(f['Definition']) for f in declared) + ' LIMIT 1')[0]
                        scenarios.append(dict(label='range', columns=None, lower=low, upper=last))
                        scenarios += [dict(label='prefix', columns=None, prefix=first[:1]),
                                      dict(label='point', columns=None, point=first)]
                        # A SQL prefix index is only a candidate filter. Select a different
                        # full value in the same prefix bucket when present.
                        if index['Fields'][0]['PrefixBytes']:
                            next_values = client.objects('JSON_ARRAY(' + encoded(declared[0]['Definition']) + ')', tail + ' ORDER BY ' + ','.join(order) + ' LIMIT 1 OFFSET 1')
                            if next_values:
                                scenarios.append(dict(label='prefix_next', columns=coverage or None, prefix=next_values[0]))
                        if not hidden:
                            scenarios.append(dict(label='reverse_limit', columns=None, reverse=True, limit=3))
                    for scenario in scenarios:
                        def sql_value(c, value):
                            if value is None:
                                return 'NULL'
                            if isinstance(value, str):
                                return "X'" + value + "'" if c['type'] in ('BINARY', 'VARBINARY') else literal(value)
                            return str(value)

                        def equality(field, value):
                            c = field['Definition']
                            return '(' + ident(c['name']) + ' <=> ' + sql_value(c, value) + ')'

                        def bound(values, greater, inclusive):
                            parts = []
                            for i, value in enumerate(values):
                                c = index['Fields'][i]['Definition']
                                col = ident(c['name'])
                                if value is None:
                                    comparison = col + ' IS NOT NULL' if greater else 'FALSE'
                                elif greater:
                                    comparison = col + ' > ' + sql_value(c, value)
                                else:
                                    comparison = '(' + col + ' IS NULL OR ' + col + ' < ' + sql_value(c, value) + ')'
                                parts.append('(' + ' AND '.join([equality(index['Fields'][j], values[j]) for j in range(i)] + [comparison]) + ')')
                            if inclusive:
                                parts.append('(' + ' AND '.join(equality(f, v) for f, v in zip(index['Fields'], values)) + ')')
                            return '(' + ' OR '.join(parts) + ')'

                        predicate = []
                        selected = scenario.get('point', scenario.get('prefix', []))
                        predicate += [equality(f, v) for f, v in zip(index['Fields'], selected)]
                        if 'lower' in scenario:
                            predicate += [bound(scenario['lower'], True, False), bound(scenario['upper'], False, True)]
                        names = scenario['columns'] or [c['name'] for c in columns]
                        projection = [next(c for c in columns if c['name'] == n) for n in names]
                        sql = tail + (' WHERE ' + ' AND '.join(predicate) if predicate else '')
                        sql += ' ORDER BY ' + ','.join(x + (' DESC' if scenario.get('reverse') else '') for x in order)
                        if scenario.get('limit'):
                            sql += ' LIMIT ' + str(scenario['limit'])
                        scenario['rows'] = client.objects('JSON_ARRAY(' + ','.join(encoded(c) for c in projection) + ')', sql)
                    expectations.append(dict(index=index['Name'], entries=entries, queries=scenarios))
                data = snapshot(client, evidence)
                ddl = '\n'.join(client.query('SHOW CREATE TABLE ' + ident(name))).split('\t', 1)[1]
            finally:
                client.query('UNLOCK TABLES')
            (out / (name + '.ibd.gz')).write_bytes(gzip.compress(data, mtime=0))
            (out / (name + '.expected.json.gz')).write_bytes(gzip.compress(json.dumps(rows, ensure_ascii=False).encode(), mtime=0))
            (out / (name + '.secondary.json.gz')).write_bytes(gzip.compress(json.dumps(expectations, ensure_ascii=False).encode(), mtime=0))
            save_json(out / (name + '.json'), schema)
            save_json(out / (name + '.indexes.json'), indexes)
            save_json(out / (name + '.source.json'), evidence)
            (out / (name + '.sql')).write_text(ddl + ';\n')
            manifest['cases'].append(dict(name=name, rows=len(rows), indexes=len(indexes), sha256=hashlib.sha256(data).hexdigest(),
                                          bytes=len(data), unordered=hidden, format=schema['row_format']))
            print(name, len(rows), 'rows', len(indexes), 'indexes', len(data), 'bytes', flush=True)

        def create(name, definition, form):
            client.query('CREATE TABLE ' + ident(name) + '(' + definition + ') ROW_FORMAT=' + form)

        for form in ('DYNAMIC', 'COMPACT'):
            tag = form.lower()
            name = 'ordinary_' + tag
            create(name, 'id INT PRIMARY KEY,n INT NULL,name VARCHAR(40) COLLATE utf8mb4_bin,vb VARBINARY(40),payload LONGTEXT,'
                         'KEY s(n DESC),UNIQUE KEY u(name),KEY px(name(3),n),KEY bp(vb(3),n),KEY cover(n,id)', form)
            client.query('INSERT INTO ' + name + " VALUES(1,NULL,NULL,X'00FF','null'),(2,NULL,NULL,X'00FF01','second'),"
                         "(3,-1,'abc-one',X'41424301',REPEAT('界😀',9000)),(4,-1,'abc-two',X'41424302','two'),"
                         "(5,2,'界😀a-one',X'',''),(6,2,'界😀a-two',NULL,NULL),(7,2,'',X'41424303','empty')")
            capture(name)
            name = 'composite_' + tag
            create(name, 'k VARBINARY(24) NOT NULL,id INT NOT NULL,n INT NULL,name VARCHAR(40) COLLATE utf8mb4_bin,'
                         'PRIMARY KEY(k,id),KEY px(k(3),name(2)),KEY dedup(n,k,id),KEY s(n)', form)
            client.query('INSERT INTO ' + name + " VALUES(X'61626301',1,2,'界😀one'),(X'61626302',2,NULL,'界😀two'),"
                         "(X'00',3,-1,NULL),(X'61626301',4,2,'abc')")
            capture(name)
            name = 'hidden_' + tag
            create(name, 'seq INT NULL,name VARCHAR(30) COLLATE utf8mb4_bin,payload LONGTEXT,KEY s(seq),KEY px(name(2))', form)
            client.query('INSERT INTO ' + name + " VALUES(4,'abc','one'),(4,'abd','two'),(NULL,NULL,NULL),(4,'abc',REPEAT('界',12000))")
            capture(name)
            name = 'unique_cluster_' + tag
            create(name, 'seq INT,k INT NOT NULL,name VARCHAR(30) COLLATE utf8mb4_bin,UNIQUE KEY actual(k),KEY s(seq),KEY px(name(2))', form)
            client.query('INSERT INTO ' + name + " VALUES(2,9,'abc'),(2,-1,'abd'),(NULL,3,NULL)")
            capture(name)
            name = 'generated_' + tag
            create(name, 'id INT PRIMARY KEY,base INT,v INT AS(base+100) VIRTUAL,s INT AS(base+1) STORED,KEY secondary(s)', form)
            client.query('INSERT INTO ' + name + '(id,base) VALUES(1,7),(2,NULL),(3,7)')
            capture(name)
            name = 'tree_' + tag
            create(name, 'id INT PRIMARY KEY,k VARBINARY(767) NOT NULL,payload VARCHAR(1200),KEY secondary(k)', form)
            for start in range(0, 800, 40):
                client.query('INSERT INTO ' + name + ' VALUES' + ','.join("(" + str(i) + ",CONCAT(REPEAT(X'FF',766),UNHEX(LPAD(HEX(" + str(i % 7) + "),2,'0'))),REPEAT('p',1100))" for i in range(start, start+40)))
            capture(name)
            name = 'empty_' + tag
            create(name, 'id INT PRIMARY KEY,n INT NULL,KEY secondary(n)', form)
            capture(name)
            for charset, collation, values in (('ascii', 'ascii_bin', ["'abc'", "'abd'", "'a'", "''"]),
                                               ('latin1', 'latin1_bin', ["CONVERT(X'808161' USING latin1)", "CONVERT(X'808162' USING latin1)", "'a'", "''"]),
                                               ('utf8', 'utf8_bin', ["'界一a'", "'界一b'", "'a'", "''"]),
                                               ('utf8mb4', 'utf8mb4_bin', ["'界😀a'", "'界😀b'", "'a'", "''"])):
                name = 'charset_' + charset + '_' + tag
                create(name, 'k CHAR(8) CHARACTER SET ' + charset + ' COLLATE ' + collation + ' NOT NULL,id INT NOT NULL,'
                             'v VARCHAR(30) CHARACTER SET ' + charset + ' COLLATE ' + collation + ',PRIMARY KEY(k,id),KEY secondary(v(2))', form)
                client.query('INSERT INTO ' + name + ' VALUES' + ','.join('(' + v + ',' + str(i+1) + ',' + v + ')' for i,v in enumerate(values)) + ",('z',5,NULL)")
                capture(name)
        manifest['status'] = 'captured'
    finally:
        (out / 'writer.sql.gz').write_bytes(gzip.compress(('\n'.join(client.log) + '\n').encode(), mtime=0))
        save_json(out / 'manifest.json', manifest)
        client.close()


if __name__ == '__main__':
    main()
