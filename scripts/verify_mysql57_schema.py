#!/usr/bin/env python3
"""Exercise unsupported real 5.7 layouts in a new independent test database."""
import argparse
import uuid
from pathlib import Path
from mysql57_schema import Client, collect, ident, save_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--out', required=True, help='new JSON evidence file')
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output must not exist')
    database = 'innodb_reader_57_reject_' + uuid.uuid4().hex[:12]
    client = Client(args.container)
    results = []
    try:
        client.query('CREATE DATABASE ' + ident(database))
        client.query('USE ' + ident(database))
        cases = [
            ('fts', 'id INT PRIMARY KEY,body TEXT,FULLTEXT KEY ft(body)', '', 'FULLTEXT'),
            ('redundant', 'id INT PRIMARY KEY', 'ROW_FORMAT=REDUNDANT', 'DYNAMIC/COMPACT'),
            ('collation', 'k VARCHAR(30) COLLATE utf8mb4_general_ci PRIMARY KEY', 'DEFAULT CHARSET=utf8mb4', 'collation'),
            ('charset', 'id INT PRIMARY KEY,payload VARCHAR(30) CHARACTER SET gbk', '', 'charset'),
        ]
        def reject(table, expected):
            try:
                collect(client, database, table)
            except ValueError as error:
                if expected not in str(error):
                    raise
                results.append(dict(table=table, rejected=True, reason=str(error)))
            else:
                raise AssertionError('unsupported schema accepted: ' + table)
        for table, definition, options, expected in cases:
            client.query('CREATE TABLE ' + ident(table) + '(' + definition + ') ' + options)
            reject(table, expected)
        client.query('ALTER TABLE fts DROP INDEX ft')
        reject('fts', 'internal stored columns')
        save_json(out, dict(database=database, results=results, sql=client.log, complete=True))
        print('Schema rejection checks:', len(results), 'passed')
    finally:
        client.close()


if __name__ == '__main__':
    main()
