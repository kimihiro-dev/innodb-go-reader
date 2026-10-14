#!/usr/bin/env python3
"""Independent fixture collector for validated MySQL 5.7 patches; optionally snapshot."""
import argparse
import gzip
import hashlib
import json
import os
import posixpath
import subprocess
import tempfile
import uuid
from pathlib import Path


def ident(value):
    if not value or '\0' in value:
        raise ValueError('empty/NUL identifier')
    return '`' + value.replace('`', '``') + '`'


def literal(value):
    return "CONVERT(X'" + value.encode().hex() + "' USING utf8mb4)"


class Client:
    def __init__(self, container):
        self.container = container
        self.log = []
        command = ['docker', 'exec', '-i']
        if 'MYSQL_PWD' in os.environ:
            command += ['--env', 'MYSQL_PWD']
        self.process = subprocess.Popen(
            command + [container, 'mysql', '--no-defaults', '-uroot',
             '--default-character-set=utf8mb4', '--max-allowed-packet=128M',
             '--batch', '--raw', '--skip-column-names', '--unbuffered', '--binary-mode'],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        self.query("SET SESSION time_zone='+00:00'")
        self.query("SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'")

    def query(self, sql):
        self.log.append(sql + ';')
        marker = 'end_' + uuid.uuid4().hex
        self.process.stdin.write(sql + ";\nSELECT '" + marker + "';\n")
        self.process.stdin.flush()
        rows = []
        while True:
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError('mysql exited before query completed')
            line = line.rstrip('\n')
            if line == marker:
                return rows
            rows.append(line)

    def objects(self, expression, tail=''):
        rows = self.query('SELECT HEX(CAST(' + expression + ' AS CHAR CHARACTER SET utf8mb4)) ' + tail)
        return [json.loads(bytes.fromhex(row)) for row in rows]

    def close(self):
        self.process.stdin.close()
        self.process.wait(timeout=10)
        self.process.stdout.close()


def collect(client, database, table):
    env = client.objects("JSON_OBJECT('version',VERSION(),'page_size',@@innodb_page_size,"
                         "'file_per_table',@@innodb_file_per_table,'checksum',@@innodb_checksum_algorithm,"
                         "'datadir',@@datadir,'charset',@@character_set_server,'collation',@@collation_server,"
                         "'sql_mode',@@sql_mode,'time_zone',@@time_zone)")[0]
    if env['version'].split('-', 1)[0] not in ('5.7.9', '5.7.10', '5.7.11', '5.7.12', '5.7.28', '5.7.43', '5.7.44') or env['page_size'] != 16384:
        raise ValueError('only MySQL 5.7.9/10/11/12/28/43/44 / 16 KiB is validated')
    where = 'WHERE TABLE_SCHEMA=' + literal(database) + ' AND TABLE_NAME=' + literal(table)
    tables = client.objects("JSON_OBJECT('engine',ENGINE,'row_format',ROW_FORMAT,'type',TABLE_TYPE)",
                            'FROM INFORMATION_SCHEMA.TABLES ' + where)
    if len(tables) != 1 or tables[0]['engine'] != 'InnoDB' or tables[0]['type'] != 'BASE TABLE':
        raise ValueError('one InnoDB base table required')
    form = tables[0]['row_format'].upper()
    if form not in ('DYNAMIC', 'COMPACT'):
        raise ValueError('only DYNAMIC/COMPACT supported')
    columns = client.objects("JSON_OBJECT('name',COLUMN_NAME,'type',DATA_TYPE,'column_type',COLUMN_TYPE,"
                             "'nullable',IS_NULLABLE,'charset',CHARACTER_SET_NAME,'collation',COLLATION_NAME,"
                             "'chars',CHARACTER_MAXIMUM_LENGTH,'bytes',CHARACTER_OCTET_LENGTH,"
                             "'precision',NUMERIC_PRECISION,'scale',NUMERIC_SCALE,'fsp',DATETIME_PRECISION,"
                             "'extra',EXTRA,'expression',GENERATION_EXPRESSION,'ordinal',ORDINAL_POSITION)",
                             'FROM INFORMATION_SCHEMA.COLUMNS ' + where + ' ORDER BY ORDINAL_POSITION')
    indexes = client.objects("JSON_OBJECT('name',i.NAME,'index_id',i.INDEX_ID,'space_id',i.SPACE,"
                             "'root_page',i.PAGE_NO,'type',i.TYPE,'fields',i.N_FIELDS,'table_id',i.TABLE_ID)",
                             'FROM INFORMATION_SCHEMA.INNODB_SYS_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_SYS_TABLES t '
                             'ON t.TABLE_ID=i.TABLE_ID WHERE t.NAME=' + literal(database + '/' + table) + ' ORDER BY i.INDEX_ID')
    cluster = [i for i in indexes if i['type'] & 1]
    if len(cluster) != 1 or cluster[0]['space_id'] == 0:
        raise ValueError('one private ordinary clustered index required; partitions/shared spaces unsupported')
    cluster = cluster[0]
    if any(i['type'] not in (0, 1, 2, 3) for i in indexes):
        raise ValueError('FULLTEXT/SPATIAL/indexed VIRTUAL layouts unsupported')
    dictionary = client.objects("JSON_OBJECT('columns',N_COLS)",
                                'FROM INFORMATION_SCHEMA.INNODB_SYS_TABLES WHERE TABLE_ID=' + str(cluster['table_id']))[0]
    spaces = client.objects("JSON_OBJECT('flags',FLAG,'page_size',PAGE_SIZE,'zip_page_size',ZIP_PAGE_SIZE,"
                            "'space_type',SPACE_TYPE)",
                            'FROM INFORMATION_SCHEMA.INNODB_SYS_TABLESPACES WHERE SPACE=' + str(cluster['space_id']))
    if len(spaces) != 1 or spaces[0]['space_type'] != 'Single' or spaces[0]['page_size'] != 16384 or spaces[0]['zip_page_size'] != 0 or spaces[0]['flags'] & ~0x7e1:
        raise ValueError('uncompressed unencrypted private 16 KiB space required')
    fields = client.objects("JSON_OBJECT('name',NAME,'position',POS)",
                            'FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS WHERE INDEX_ID=' + str(cluster['index_id']) + ' ORDER BY POS')
    stats = client.objects("JSON_OBJECT('name',INDEX_NAME,'column',COLUMN_NAME,'position',SEQ_IN_INDEX,"
                           "'prefix',SUB_PART,'order',COLLATION,'unique',NON_UNIQUE=0,'type',INDEX_TYPE)",
                           'FROM INFORMATION_SCHEMA.STATISTICS ' + where + ' ORDER BY INDEX_NAME,SEQ_IN_INDEX')
    files = client.objects("JSON_OBJECT('path',PATH)",
                           'FROM INFORMATION_SCHEMA.INNODB_SYS_DATAFILES WHERE SPACE=' + str(cluster['space_id']))
    schema = dict(row_format=form, columns=[], **{k: cluster[k] for k in ('root_page', 'index_id', 'space_id')})
    virtual = []
    supported = {'TINYINT', 'SMALLINT', 'MEDIUMINT', 'INT', 'BIGINT', 'DECIMAL', 'FLOAT', 'DOUBLE',
                 'DATE', 'YEAR', 'DATETIME', 'TIME', 'TIMESTAMP', 'BIT', 'CHAR', 'VARCHAR', 'BINARY', 'VARBINARY',
                 'ENUM', 'SET', 'JSON', 'TINYTEXT', 'TEXT', 'MEDIUMTEXT', 'LONGTEXT', 'TINYBLOB', 'BLOB',
                 'MEDIUMBLOB', 'LONGBLOB', 'GEOMETRY', 'POINT', 'LINESTRING', 'POLYGON', 'MULTIPOINT',
                 'MULTILINESTRING', 'MULTIPOLYGON', 'GEOMETRYCOLLECTION'}
    for c in columns:
        if 'VIRTUAL GENERATED' in c['extra']:
            virtual.append(dict(name=c['name'], ordinal=c['ordinal'], expression=c['expression']))
            continue
        typ = c['type'].upper()
        if typ not in supported:
            raise ValueError('unsupported column type: ' + typ)
        out = dict(name=c['name'], type=typ, nullable=c['nullable'] == 'YES')
        if 'unsigned' in c['column_type'].split():
            out['unsigned'] = True
        if c['charset']:
            if c['charset'] not in ('utf8mb4', 'utf8', 'ascii', 'latin1'):
                raise ValueError('unsupported charset: ' + c['charset'])
            out['charset'] = c['charset']
        if typ in ('CHAR', 'VARCHAR'):
            out['max_chars'] = c['chars']
        if typ in ('BINARY', 'VARBINARY'):
            out['max_bytes'] = c['bytes']
        if typ == 'DECIMAL':
            out.update(precision=c['precision'], scale=c['scale'])
        if typ in ('DATETIME', 'TIME', 'TIMESTAMP'):
            out['fsp'] = c['fsp']
        if typ == 'BIT':
            out['bit_length'] = c['precision']
        if typ in ('ENUM', 'SET'):
            # Reuse MySQL's own literal decoder; COLUMN_TYPE is server-produced SQL.
            values = client.objects('JSON_ARRAY' + c['column_type'][c['column_type'].index('('):])[0]
            out['enum_values' if typ == 'ENUM' else 'set_values'] = values
        if 'STORED GENERATED' in c['extra']:
            out['generation_expression'] = c['expression']
        schema['columns'].append(out)
    if virtual:
        schema['virtual_columns'] = virtual
    if dictionary['columns'] != len(schema['columns']) + 3:
        raise ValueError('internal stored columns are absent from SQL metadata')
    keys = [s for s in stats if s['name'] == cluster['name']]
    if cluster['name'] == 'GEN_CLUST_INDEX' and not keys and not fields:
        schema['clustered_key'] = dict(name=cluster['name'], hidden_row_id=True)
    else:
        if not keys or any(k['prefix'] is not None or k['type'] != 'BTREE' or k['order'] != 'A' for k in keys):
            raise ValueError('complete ascending BTREE clustered key required')
        names = [k['column'] for k in keys]
        if [f['name'] for f in fields] != names:
            raise ValueError('clustered dictionary and SQL key fields disagree')
        if cluster['name'] == 'PRIMARY':
            schema['primary_keys'] = names
        else:
            schema['clustered_key'] = dict(name=cluster['name'], columns=names)
        for c in schema['columns']:
            if c['name'] in names and c.get('charset'):
                collation = next(x['collation'] for x in columns if x['name'] == c['name'])
                if collation not in ('utf8mb4_bin', 'utf8_bin', 'ascii_bin', 'latin1_bin'):
                    raise ValueError('unsupported clustered collation: ' + collation)
                c['collation'] = 'utf8mb3_bin' if collation == 'utf8_bin' else collation
    evidence = dict(environment=env, database=database, table=table, columns=columns, indexes=indexes,
                    clustered_fields=fields, statistics=stats, tablespace=spaces[0], datafiles=files, dictionary=dictionary)
    return schema, evidence


def snapshot(client, evidence):
    files = evidence['datafiles']
    if len(files) != 1:
        raise ValueError('one physical datafile required')
    source = files[0]['path']
    if not posixpath.isabs(source):
        source = posixpath.join(evidence['environment']['datadir'], source)
    with tempfile.TemporaryDirectory(prefix='innodb-mysql57-') as temp:
        path = Path(temp) / 'table.ibd'
        subprocess.run(['docker', 'cp', client.container + ':' + source, str(path)], check=True, stdout=subprocess.DEVNULL)
        return path.read_bytes()


def collect_indexes(client, schema, evidence):
    """Reconstruct cached physical fields using 5.7 dict_index_build_internal_non_clust."""
    result = []
    evidence['secondary_fields'] = {}
    columns = schema['columns']
    positions = {c['name']: i for i, c in enumerate(columns)}
    key_types = {'TINYINT', 'SMALLINT', 'MEDIUMINT', 'INT', 'BIGINT', 'DECIMAL', 'BIT',
                 'DATE', 'YEAR', 'DATETIME', 'TIME', 'TIMESTAMP', 'CHAR', 'VARCHAR', 'BINARY', 'VARBINARY'}
    widths = {'utf8mb4': 4, 'utf8': 3, 'ascii': 1, 'latin1': 1}

    def field(name, prefix=None):
        if name not in positions:
            raise ValueError('indexed VIRTUAL/internal column unsupported: ' + name)
        c = dict(columns[positions[name]])
        if c['type'] not in key_types:
            raise ValueError('unsupported secondary key type: ' + c['type'])
        width = 1
        if c['type'] in ('CHAR', 'VARCHAR'):
            raw = next(x for x in evidence['columns'] if x['name'] == name)
            collation = raw['collation']
            if collation not in ('utf8mb4_bin', 'utf8_bin', 'ascii_bin', 'latin1_bin'):
                raise ValueError('unsupported secondary collation: ' + collation)
            c['collation'] = 'utf8mb3_bin' if collation == 'utf8_bin' else collation
            width = widths[c['charset']]
        length = 0
        if prefix is not None:
            if c['type'] not in ('CHAR', 'VARCHAR', 'BINARY', 'VARBINARY') or prefix <= 0:
                raise ValueError('unsupported prefix')
            maximum = c.get('max_chars', c.get('max_bytes'))
            if prefix > maximum:
                raise ValueError('prefix exceeds full width')
            if prefix < maximum:
                length = prefix * width
        return dict(Column=positions[name], Definition=c, PrefixBytes=length, RowID=False)

    cluster = schema.get('clustered_key', {})
    if cluster.get('hidden_row_id'):
        locator = [dict(Column=-1, Definition=dict(name='DB_ROW_ID', type='BINARY', max_bytes=6),
                        PrefixBytes=0, RowID=True)]
    else:
        locator = [field(name) for name in schema.get('primary_keys', cluster.get('columns', []))]
    for index in evidence['indexes']:
        if index['type'] & 1:
            continue
        keys = [s for s in evidence['statistics'] if s['name'] == index['name']]
        raw = client.objects("JSON_OBJECT('name',NAME,'position',POS)",
                             'FROM INFORMATION_SCHEMA.INNODB_SYS_FIELDS WHERE INDEX_ID=' + str(index['index_id']) + ' ORDER BY POS')
        evidence['secondary_fields'][index['name']] = raw
        if (not keys or len(keys) != index['fields'] or [f['name'] for f in raw] != [k['column'] for k in keys]
                or any(k['type'] != 'BTREE' or k['order'] != 'A' or k['position'] != i+1
                       or bool(k['unique']) != (index['type'] == 2) for i, k in enumerate(keys))):
            raise ValueError('secondary dictionary and SQL declarations disagree: ' + index['name'])
        fields = [field(k['column'], k['prefix']) for k in keys]
        located = []
        for f in locator:
            found = next((i for i, old in enumerate(fields) if old['Column'] == f['Column']
                          and old['RowID'] == f['RowID'] and old['PrefixBytes'] == 0), None)
            if found is None:
                found = len(fields)
                fields.append(f)
            located.append(found)
        result.append(dict(Name=index['name'], Unique=index['type'] == 2, RowFormat=schema['row_format'],
                           SpaceID=index['space_id'], IndexID=index['index_id'], RootPage=index['root_page'],
                           Fields=fields, UserFields=len(keys), ClusteredFields=located))
    return result


def save_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', required=True)
    parser.add_argument('--database', required=True)
    parser.add_argument('--table', required=True)
    parser.add_argument('--out', required=True, help='new output directory')
    parser.add_argument('--snapshot', action='store_true', help='hold FOR EXPORT lock and copy the .ibd')
    parser.add_argument('--indexes', action='store_true', help='also collect ordinary secondary indexes.json')
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output directory must not exist')
    out.mkdir(parents=True)
    client = Client(args.container)
    target = ident(args.database) + '.' + ident(args.table)
    locked = False
    try:
        if args.snapshot:
            client.query('FLUSH TABLES ' + target + ' FOR EXPORT')
            locked = True
        schema, evidence = collect(client, args.database, args.table)
        indexes = collect_indexes(client, schema, evidence) if args.indexes else None
        if args.snapshot:
            data = snapshot(client, evidence)
            (out / 'table.ibd.gz').write_bytes(gzip.compress(data, mtime=0))
            evidence.update(sha256=hashlib.sha256(data).hexdigest(), bytes=len(data), capture='FOR EXPORT lock held')
        else:
            evidence['capture'] = 'metadata only; caller must prevent concurrent DDL and pair a stable snapshot'
        save_json(out / 'schema.json', schema)
        if indexes is not None:
            save_json(out / 'indexes.json', indexes)
        save_json(out / 'source.json', evidence)
    finally:
        if locked:
            client.query('UNLOCK TABLES')
        client.close()


if __name__ == '__main__':
    main()
