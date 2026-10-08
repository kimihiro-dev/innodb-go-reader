#!/usr/bin/env python3
"""Capture spatial values in a new MySQL 8.0.45 database; password via MYSQL_PWD."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import uuid
from datetime import datetime, timezone


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mysql', required=True)
    parser.add_argument('--socket', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    out = Path(args.out).resolve()
    if out.exists():
        parser.error('output must not exist')
    out.mkdir(parents=True)
    database = 'innodb_reader_geometry_' + uuid.uuid4().hex[:12]
    proc = subprocess.Popen([args.mysql, '--no-defaults', '-uroot', '--socket='+args.socket,
                             '--default-character-set=utf8mb4', '--max-allowed-packet=128M',
                             '--batch', '--raw', '--skip-column-names', '--unbuffered'],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, encoding='utf-8', env=os.environ.copy())
    log = []

    def query(sql):
        log.append(sql+';')
        marker = '__end_'+uuid.uuid4().hex
        proc.stdin.write(sql+";\nSELECT '"+marker+"';\n")
        proc.stdin.flush()
        lines = []
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError('mysql exited; see generate.sql.gz')
            line = line.rstrip('\n')
            if line == marker:
                return lines
            lines.append(line)

    def save(name, value):
        data = (json.dumps(value, ensure_ascii=False, indent=2)+'\n').encode()
        (out/name).write_bytes(gzip.compress(data, mtime=0))

    def geom(wkt, srid=0):
        return "ST_GeomFromText('"+wkt+"',"+str(srid)+(", 'axis-order=long-lat'" if srid else "")+")"

    wkts = ['POINT(12.5 -7.25)', 'LINESTRING(0 0,1 2,3 4)',
        'POLYGON((0 0,8 0,8 8,0 8,0 0),(1 1,1 2,2 2,2 1,1 1))',
        'MULTIPOINT((1 2),(3 4))', 'MULTILINESTRING((0 0,1 1),(2 2,3 3))',
        'MULTIPOLYGON(((0 0,2 0,2 2,0 0)),((3 3,5 3,5 5,3 3)))',
        'GEOMETRYCOLLECTION(POINT(1 2),GEOMETRYCOLLECTION(LINESTRING(0 0,1 1)))']
    types = ['POINT','LINESTRING','POLYGON','MULTIPOINT','MULTILINESTRING','MULTIPOLYGON','GEOMETRYCOLLECTION']
    cases = [('geometry_lesson','GEOMETRY',None,['NULL']+[geom(w) for w in wkts]+[geom('GEOMETRYCOLLECTION EMPTY'),geom('POINT(120 30)',4326)] )]
    cases += [('geometry_'+typ.lower(),typ,None,['NULL',geom(w)]) for typ,w in zip(types,wkts)]
    cases += [('geometry_srid','POINT',4326,[geom('POINT(120 30)',4326),geom('POINT(-70 -20)',4326)]),
        ('geometry_zero','POINT',0,[geom('POINT(1 2)')]),
        ('geometry_large','GEOMETRY',None,[geom('LINESTRING('+','.join(str(i)+' '+str(i%13) for i in range(6000))+')')]),
        ('geometry_tree','GEOMETRY',None,[geom(wkts[i%7]) for i in range(600)])]
    manifest = dict(database=database, generated_at=datetime.now(timezone.utc).isoformat(), cases=[], status='incomplete')
    try:
        env = json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'datadir',@@datadir,'page_size',@@innodb_page_size,'checksum',@@innodb_checksum_algorithm,'file_per_table',@@innodb_file_per_table)")[0])
        manifest['environment'] = env
        if env['version']!='8.0.45' or env['page_size']!=16384 or not env['file_per_table'] or env['checksum'] not in ('crc32','strict_crc32'):
            raise RuntimeError('unsupported fixture instance configuration')
        query("SET SESSION sql_mode='STRICT_TRANS_TABLES'")
        query("SET SESSION time_zone='+00:00'")
        env['sql_mode'] = 'STRICT_TRANS_TABLES'
        env['time_zone'] = '+00:00'
        query(f'CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci')
        query(f'USE `{database}`')
        for name, typ, srid, exprs in cases:
            query(f'CREATE TABLE `{name}` (id INT NOT NULL PRIMARY KEY, doc {typ} NULL{(' SRID '+str(srid)) if srid is not None else ''}, note VARCHAR(32) NULL) ENGINE=InnoDB ROW_FORMAT=DYNAMIC')
            # Append another ordinary column to detect geometry boundary mistakes.
            for start in range(0,len(exprs),100):
                query(f'INSERT INTO `{name}` VALUES '+','.join(f"({i},{e},'after-geometry')" for i,e in enumerate(exprs[start:start+100],start+1)))
            query(f'FLUSH TABLES `{name}` FOR EXPORT')
            try:
                expected = [json.loads(x) for x in query(f"SELECT JSON_OBJECT('id',id,'sql_null',doc IS NULL,'raw',HEX(doc),'wkb',HEX(ST_AsBinary(doc,'axis-order=long-lat')),'default_wkb',HEX(ST_AsBinary(doc)),'srid',ST_SRID(doc),'type',ST_GeometryType(doc),'geo',ST_AsGeoJSON(doc,17,0),'note',note) FROM `{name}` ORDER BY id")]
                index = json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',i.INDEX_ID,'space_id',i.SPACE) FROM INFORMATION_SCHEMA.INNODB_INDEXES i JOIN INFORMATION_SCHEMA.INNODB_TABLES t ON t.TABLE_ID=i.TABLE_ID "+f"WHERE t.NAME='{database}/{name}' AND i.NAME='PRIMARY'")[0])
                ddl = '\n'.join(query(f'SHOW CREATE TABLE `{name}`'))
                (out/(name+'.sql')).write_text(ddl+'\n', encoding='utf-8')
                data = (Path(env['datadir'])/database/(name+'.ibd')).read_bytes()
            finally:
                query('UNLOCK TABLES')
            (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
            schema = dict(columns=[dict(name='id',type='INT'),dict(name='doc',type=typ,nullable=True,**({'srid':srid} if srid is not None else {})),dict(name='note',type='VARCHAR',nullable=True,max_chars=32)],primary_key='id',**index)
            (out/(name+'.json')).write_text(json.dumps(schema,indent=2)+'\n')
            save(name+'.expected.json.gz',expected)
            manifest['cases'].append(dict(name=name,rows=len(expected),bytes=len(data),sha256=hashlib.sha256(data).hexdigest(),**index))
        manifest['status']='captured'
    finally:
        (out/'generate.sql.gz').write_bytes(gzip.compress(('\n'.join(log)+'\n').encode(),mtime=0))
        (out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
        proc.stdin.close()
        try: proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.terminate()
            proc.wait(timeout=15)
        proc.stdout.close()
    print(json.dumps(manifest))


if __name__=='__main__':
    main()
