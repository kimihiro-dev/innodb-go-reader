#!/usr/bin/env python3
"""Exercise real unsupported secondary definitions; retain independent evidence."""
import argparse
import uuid
from pathlib import Path
from mysql57_schema import Client, collect, collect_indexes, ident, save_json


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--container', required=True)
    p.add_argument('--out', required=True)
    args = p.parse_args()
    out = Path(args.out)
    if out.exists():
        p.error('output must not exist')
    client = Client(args.container)
    database = 'innodb_reader_57b_reject_' + uuid.uuid4().hex[:12]
    results = []
    try:
        client.query('CREATE DATABASE ' + ident(database) + ' CHARACTER SET utf8mb4 COLLATE utf8mb4_bin')
        client.query('USE ' + ident(database))
        for name, declaration, reason in [
            ('collation', 'v VARCHAR(20) COLLATE utf8mb4_general_ci,KEY s(v)', 'collation'),
            ('floating', 'v DOUBLE,KEY s(v)', 'key type'),
            ('blob', 'v BLOB,KEY s(v(10))', 'key type'),
            ('virtual', 'v INT,g INT AS(v+1) VIRTUAL,KEY s(g)', 'indexed VIRTUAL'),
        ]:
            client.query('CREATE TABLE ' + ident(name) + '(id INT PRIMARY KEY,' + declaration + ') ROW_FORMAT=DYNAMIC')
            try:
                schema, evidence = collect(client, database, name)
                collect_indexes(client, schema, evidence)
            except ValueError as error:
                if reason not in str(error):
                    raise
                results.append(dict(table=name, rejected=True, reason=str(error)))
            else:
                raise AssertionError('unsupported definition accepted')
        # Full-width prefix becomes a complete field; clustered locator is reused.
        client.query('CREATE TABLE overlap(k VARBINARY(8) NOT NULL,id INT NOT NULL,PRIMARY KEY(k,id),KEY s(k(8),id))')
        schema, evidence = collect(client, database, 'overlap')
        indexes = collect_indexes(client, schema, evidence)
        assert len(indexes[0]['Fields']) == 2 and indexes[0]['ClusteredFields'] == [0, 1]
        results.append(dict(table='overlap', complete_prefix=True, indexes=indexes, source=evidence))
        save_json(out, dict(database=database, results=results, sql=client.log, complete=True))
        print(len(results), 'real collector boundary checks passed')
    finally:
        client.close()


if __name__ == '__main__':
    main()
