#!/usr/bin/env python3
"""Generate single-leaf, --tree, --integers, --variable, --lob, --large-lob, --decimal, --floating, --dates, --datetimes, --times, --timestamps, --bits, --fixed-binary, --enums, --sets or --chars fixtures. Requires mysql and MYSQL_PWD; never drops data."""
import argparse
import hashlib
import gzip
import random
import struct
import json
import os
from pathlib import Path
import shutil
import subprocess
import uuid
from datetime import datetime, timezone


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mysql', required=True)
    parser.add_argument('--socket', required=True)
    parser.add_argument('--user', default='root')
    parser.add_argument('--out', required=True, help='New output directory (must not exist)')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--chars', action='store_true', help='Generate utf8mb4 CHAR and physical padding fixtures')
    mode.add_argument('--sets', action='store_true', help='Generate SET dictionaries, masks and mixed tree fixtures')
    mode.add_argument('--enums', action='store_true', help='Generate ENUM dictionary, ordinal and mixed tree fixtures')
    mode.add_argument('--fixed-binary', action='store_true', help='Generate fixed BINARY widths and padding fixtures')
    mode.add_argument('--bits', action='store_true', help='Generate BIT widths 1..64 and mixed tree fixtures')
    mode.add_argument('--timestamps', action='store_true', help='Generate UTC TIMESTAMP and timezone fixtures')
    mode.add_argument('--times', action='store_true', help='Generate signed TIME fractional-second fixtures')
    mode.add_argument('--datetimes', action='store_true', help='Generate DATETIME fractional-second fixtures')
    mode.add_argument('--dates', action='store_true', help='Generate DATE/YEAR fixtures including permissive date components')
    mode.add_argument('--floating', action='store_true', help='Generate finite FLOAT/DOUBLE fixtures')
    mode.add_argument('--decimal', action='store_true', help='Generate exact DECIMAL fixtures')
    mode.add_argument('--large-lob', action='store_true', help='Generate TEXT/BLOB and multi-index LOB fixtures')
    mode.add_argument('--lob', action='store_true', help='Generate initial noncompressed external value fixtures')
    mode.add_argument('--variable', action='store_true', help='Generate inline variable and external rejection fixtures')
    mode.add_argument('--integers', action='store_true', help='Generate compressed integer fixtures')
    mode.add_argument('--tree', action='store_true', help='Generate compressed multi-page/tree fixtures')
    args = parser.parse_args()
    out = Path(args.out).resolve()
    if out.exists():
        parser.error('output directory already exists; choose a new directory')
    db = 'innodb_reader_fixture_' + uuid.uuid4().hex[:12]
    out.mkdir(parents=True)
    # stderr goes directly to the terminal: no unread pipe, and failures cannot deadlock.
    proc = subprocess.Popen([args.mysql, '--no-defaults', '--user=' + args.user,
                             '--socket=' + args.socket, '--default-character-set=utf8mb4',
                             '--max-allowed-packet=128M',
                             '--batch', '--raw', '--skip-column-names', '--unbuffered'],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            text=True, encoding='utf-8', env=os.environ.copy())

    def query(sql):
        marker = '__fixture_end_' + uuid.uuid4().hex
        proc.stdin.write(sql.rstrip(';') + ';\nSELECT "' + marker + '";\n')
        proc.stdin.flush()
        lines = []
        while True:
            line = proc.stdout.readline()
            if not line:
                raise RuntimeError('mysql exited before query completed')
            line = line.rstrip('\n')
            if line == marker:
                return lines
            lines.append(line)

    def column(name, kind, nullable=False, max_chars=0):
        return dict(name=name, type=kind, nullable=nullable, max_chars=max_chars)

    cols = [column('id', 'INT'), column('score', 'INT', True),
            column('name', 'VARCHAR', True, 32)]
    cases = [
        ('lesson_rows', cols, "(7,0,''),(-3,42,'InnoDB'),(12,-5,'你好'),(2,NULL,NULL)"),
        ('empty_rows', cols, None),
        ('single_row', cols, "(1,NULL,'单行😀')"),
        ('boundary_rows', cols,
         "(-2147483648,2147483647,REPEAT('😀',32)),"
         "(2147483647,-2147483648,REPEAT('a',32)),(0,0,NULL),"
         "(-1,NULL,''),(1,NULL,'非空'),(2,5,'')"),
        ('null_bitmap_rows', [column('n' + str(i), 'INT', True) for i in range(9)]
         + [column('name','VARCHAR',True,32), column('id','INT')],
         "(NULL,1,NULL,3,NULL,5,NULL,7,NULL,'九列',2),"
         "(0,NULL,2,NULL,4,NULL,6,NULL,8,NULL,-2)"),
        ('directory_rows', cols, ','.join(f"({i},{i},'row{i}')" for i in range(39,-1,-1))),
        ('variable_rows', [column('id','INT'), column('left_text','VARCHAR',True,63),
                           column('right_text','VARCHAR',True,32)],
         "(3,REPEAT('😀',63),'终'),(1,'左','右'),(2,NULL,''),(4,'',NULL)")]
    if args.tree:
        ordered = list(range(-500, 500))
        shuffled = list(range(-750, 750))
        random.Random(8045).shuffle(shuffled)
        cases = [('ordered_rows', cols, ordered), ('shuffled_rows', cols, shuffled),
                 ('deep_rows', [column('id','INT')] +
                  [column('v'+str(i),'VARCHAR',False,63) for i in range(8)], list(range(12000)))]
    if args.integers:
        types = [('TINYINT',8), ('SMALLINT',16), ('MEDIUMINT',24), ('INT',32), ('BIGINT',64)]
        mixed = []
        limits = []
        for kind, bits in types:
            for unsigned in (False, True):
                c = column(kind.lower()+('_u' if unsigned else '_s'),kind,True)
                c['unsigned'] = unsigned
                mixed.append(c)
                limits.append((0 if unsigned else -(1<<(bits-1)), (1<<bits)-1 if unsigned else (1<<(bits-1))-1))
        cases = []
        for (kind,bits) in types:
            for unsigned in (False, True):
                lo = 0 if unsigned else -(1<<(bits-1))
                hi = (1<<bits)-1 if unsigned else (1<<(bits-1))-1
                keys = sorted(set([lo,lo+1,hi-1,hi]+list(range(0,200 if unsigned else 100))+([] if unsigned else list(range(-100,0)))))
                # Include both sides of the signed 64-bit boundary in unsigned BIGINT.
                if bits == 64 and unsigned:
                    keys = sorted(set(keys + list(range((1<<63)-100,(1<<63)+100)) + list(range(hi-100,hi+1))))
                elif bits == 64:
                    keys = sorted(set(keys + list(range(lo,lo+100)) + list(range(hi-100,hi+1))))
                random.Random(8045).shuffle(keys)
                keycol = column('id',kind)
                keycol['unsigned'] = unsigned
                columns = mixed[:5] + [keycol] + mixed[5:] + [column('text','VARCHAR',True,63)]
                tuples = []
                for key in keys:
                    vals = [('NULL' if key%5==0 else str((a,b,0)[key%3])) for a,b in limits]
                    vals.insert(5,str(key))
                    vals.append("REPEAT('😀',63)" if key%7 else 'NULL')
                    tuples.append('('+','.join(vals)+')')
                cases.append((kind.lower()+('_unsigned' if unsigned else '_signed'), columns, tuples))
    if args.variable:
        def binary(name, maximum, nullable=True):
            c = column(name, 'VARBINARY', nullable)
            c['max_bytes'] = maximum
            return c

        pattern = bytes(range(256)) * 8
        def blob(length):
            return "X'" + pattern[:length].hex() + "'"

        columns = [column('short_text','VARCHAR',True,63), column('long_text','VARCHAR',True,1024),
                   column('id','INT'), binary('b255',255), binary('b256',256), binary('payload',2048)]
        values = []
        for key, length in enumerate([0,1,127,128,255,256,300,1024]):
            values.append('('+','.join(["REPEAT('😀',63)",f"REPEAT('a',{length})",str(key),
                          blob(min(length,255)),blob(min(length,256)),blob(length)])+')')
        values += ["(NULL,NULL,8,NULL,NULL,NULL)", "('',CONCAT('中文😀',CHAR(0),'  '),9,X'',X'00FF80',X'FFFE000020')"]
        bitmap = [column('v'+str(i),'VARCHAR',True,128) if i%2==0 else binary('v'+str(i),512) for i in range(10)]
        bitmap_values = []
        for key in range(3):
            fields = [('NULL' if (i+key)%3==0 else ("REPEAT('😀',64)" if i%2==0 else blob(128+i))) for i in range(10)]
            bitmap_values.append('('+','.join(fields+[str(key)])+')')
        keys=list(range(350)); random.Random(8045).shuffle(keys)
        tree_values=[]
        for key in keys:
            length=[0,127,128,255,256,1024][key%6]
            tree_values.append('('+','.join(["NULL" if key%7==0 else "REPEAT('😀',63)",
                "NULL" if key%11==0 else f"REPEAT('界',{length})",str(key),blob(min(length,255)),
                "NULL" if key%5==0 else blob(min(length,256)),blob(length)])+')')
        cases = [('length_rows',columns,values),
                 ('bitmap_rows',bitmap+[column('id','INT')],bitmap_values),
                 ('variable_tree',columns,tree_values),
                 ('large_declared',[column('id','INT'),column('text','VARCHAR',True,12000),binary('data',16000)],
                  ["(0,REPEAT('a',6000),REPEAT(X'FF',1000))","(1,'',X'')"]),
                 ('empty_variable',columns,[]),
                 ('external_text',[column('id','INT'),column('text','VARCHAR',True,12000)],
                  ["(0,'inline')","(1,REPEAT('😀',10000))"]),
                 ('external_binary',[column('id','INT'),binary('data',60000)],
                  ["(0,X'00FF')","(1,REPEAT(X'FF0080',10000))"])]
    if args.lob:
        def lob_binary(name,maximum):
            c=column(name,'VARBINARY',True);c['max_bytes']=maximum;return c
        binary_cols=[column('id','INT'),lob_binary('data',60000)]
        sizes=[0,9000,15680,15681,32007,32008,60000]
        size_values=[f"({i},LEFT(REPEAT(X'00FF80',20000),{length}))" for i,length in enumerate(sizes)]
        size_values.append('(7,NULL)')
        keycol=column('id','BIGINT');keycol['unsigned']=True
        mixed_cols=[column('text','VARCHAR',True,6000),keycol,lob_binary('data',30000),column('tag','VARCHAR',False,128)]
        keys=list(range(80));random.Random(8045).shuffle(keys)
        mixed=[]
        for key in keys:
            fields=['NULL' if key%11==0 else "REPEAT('界',6000)",str((1<<63)+key),
                    'NULL' if key%13==0 else "REPEAT(X'FF008020',5000)","REPEAT('😀',128)"]
            mixed.append('('+','.join(fields)+')')
        cases=[('lob_sizes',binary_cols,size_values),
               ('lob_text',[column('id','INT'),column('text','VARCHAR',True,12000)],
                ["(0,'')","(1,REPEAT('😀',10000))","(2,NULL)","(3,REPEAT('界',11000))"]),
               ('lob_mixed_tree',mixed_cols,mixed)]
    if args.large_lob:
        types=['TINYTEXT','TINYBLOB','TEXT','BLOB','MEDIUMTEXT','MEDIUMBLOB','LONGTEXT','LONGBLOB']
        columns=[column('id','INT')]+[column(t.lower(),t,True) for t in types]
        values=[]
        for key,length in enumerate([0,1,127,128,255]):
            fields=[str(key)]+[f"REPEAT('a',{length})" if 'TEXT' in t else f"LEFT(REPEAT(X'00FF80',85),{length})" for t in types]
            values.append('('+','.join(fields)+')')
        values.append('(5,'+','.join(['NULL']*8)+')')
        values.append('(6,'+','.join("REPEAT('😀',63)" if 'TEXT' in t else "X'FFFE000020'" for t in types)+')')
        ten=15680+9*16327
        pagefull=15680+281*16327
        cases=[('all_lob_types',columns,values),
               ('text_blob',[column('id','INT'),column('text','TEXT',True),column('data','BLOB',True)],
                ["(0,NULL,NULL)","(1,'',X'')","(2,REPEAT('界',20000),REPEAT(X'FF0080',20000))"]),
               ('medium_index',[column('id','INT'),column('data','MEDIUMBLOB',True)],
                [f"({i},LEFT(REPEAT(X'00FF80',{(length+2)//3}),{length}))" for i,length in enumerate([ten,ten+1,pagefull,pagefull+1])]),
               ('large_text',[column('id','INT'),column('medium','MEDIUMTEXT',True),column('long','LONGTEXT',True)],
                ["(0,REPEAT('界',100000),REPEAT('😀',500000))"]),
               ('long_limit',[column('id','INT'),column('data','LONGBLOB',True)],
                ["(0,REPEAT(X'FF008020',4194304))"]),
               ('long_over_limit',[column('id','INT'),column('data','LONGBLOB',True)],
                ["(0,CONCAT(REPEAT(X'FF008020',4194304),X'00'))"])]
    if args.decimal:
        def decimal(name, precision, scale, unsigned=False):
            c=column(name,'DECIMAL',True)
            c.update(precision=precision,scale=scale,unsigned=unsigned)
            return c
        def value(p,s,which):
            high=('9'*(p-s) or '0')+('.'+'9'*s if s else '')
            step=('0.'+'0'*(s-1)+'1') if s else '1'
            return [high,'-'+high,'0',step,'-'+step,'NULL'][which]
        pairs=[(p,s) for p in range(1,66) for s in range(min(p,30)+1)]
        cases=[]
        for start in range(0,len(pairs),100):
            batch=pairs[start:start+100]
            columns=[decimal('d'+str(p)+'_'+str(s),p,s) for p,s in batch]
            mid=len(columns)//2
            columns.insert(mid,column('id','INT'))
            tuples=[]
            for k in range(6):
                vals=[value(p,s,k) for p,s in batch];vals.insert(mid,str(k))
                tuples.append('('+','.join(vals)+')')
            cases.append(('decimal_matrix_'+str(start//100),columns,tuples))
        cases.append(('decimal_unsigned',[column('id','INT'),decimal('amount',65,30,True),decimal('fraction',30,30,True)],
                      [f"({k},{value(65,30,k)},{value(30,30,k)})" for k in [0,2,3,5]]))
        cases.append(('decimal_lesson',[decimal('amount',14,4),column('note','VARCHAR',True,40),column('id','INT'),decimal('tiny',2,2),column('body','TEXT',True)],
                      ["(1234567890.1234,'正数',1,0.01,NULL)","(-1234567890.1234,'负数',2,-0.99,REPEAT('界',20000))","(0.0000,'零',3,0.00,'')","(NULL,NULL,4,NULL,NULL)"]))
        keys=list(range(-300,300));random.Random(8045).shuffle(keys)
        cases.append(('decimal_tree',[decimal('amount',65,30),column('id','BIGINT'),column('note','VARCHAR',True,100),decimal('whole',65,0,True)],
                      [f"({value(65,30,k%6)},{k},REPEAT('界',100),{value(65,0,[0,2,3,5][k%4])})" for k in keys]))
    if args.floating:
        def floating(name,kind,unsigned=False):
            c=column(name,kind,True);c['unsigned']=unsigned;return c
        cases=[]
        for kind,bits,exp,frac in [('FLOAT',32,8,23),('DOUBLE',64,11,52)]:
            sign=1<<(bits-1);norm=1<<frac;maximum=(((1<<exp)-1)<<frac)-1
            one=(127 if bits==32 else 1023)<<frac
            patterns=[0,sign,1,sign|1,norm-1,norm,maximum,sign|maximum,one-1,one,one+1,sign|one]
            rng=random.Random(8045+bits)
            while len(patterns)<140:
                u=rng.getrandbits(bits)
                if ((u>>frac)&((1<<exp)-1))!=(1<<exp)-1:patterns.append(u)
            tuples=[]
            for i,u in enumerate(patterns):
                v=struct.unpack('<f' if bits==32 else '<d',u.to_bytes(bits//8,'little'))[0]
                tuples.append(f"({i},CAST('{repr(v)}' AS {kind}),CAST('{repr(abs(v))}' AS {kind}))")
            for text in ['0.1',str((1<<(24 if bits==32 else 53))+1)]:
                tuples.append(f"({len(tuples)},CAST('{text}' AS {kind}),CAST('{text}' AS {kind}))")
            tuples.append(f"({len(tuples)},NULL,NULL)")
            cases.append((kind.lower()+'_values',[column('id','INT'),floating('value',kind),floating('positive',kind,True)],tuples))
        mixed=[floating('f'+str(i),'FLOAT' if i%2==0 else 'DOUBLE') for i in range(10)]
        mixed.insert(5,column('id','INT'))
        dec=column('exact','DECIMAL',True);dec.update(precision=14,scale=4)
        mixed+=[dec,column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if (key==3 or (key==2 and i%2==0)) else ('-0.1e0' if i%2 else '0.1e0') for i in range(10)]
            vals.insert(5,str(key))
            vals+=['NULL' if key==3 else '1234567890.1234',"'混合😀'", "REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('float_mixed',mixed,rows))
        keys=list(range(-300,300));random.Random(8045).shuffle(keys)
        cases.append(('float_tree',[floating('f','FLOAT'),column('id','BIGINT'),floating('d','DOUBLE'),column('note','VARCHAR',True,100)],
                      [f"({k}.125e0,{k},{k}.0625e0,REPEAT('界',100))" for k in keys]))
    if args.dates:
        dates=[None,'0000-00-00','0000-01-01','0001-01-01','0999-12-31','1000-01-01','1900-02-28','1900-02-29','2000-02-29','2024-02-29','2023-02-31','2024-00-15','2024-05-00','9999-12-31']
        date_values=[f"({i},"+('NULL' if d is None else "'"+d+"'")+")" for i,d in enumerate(dates)]
        year_values=[f"({i},{0 if i==0 else 1900+i})" for i in range(256)]+['(256,NULL)']
        components=[]
        for year in [1900,2000,2024,9999]:
            for month in range(13):
                for day in range(32):
                    i=len(components)
                    components.append(f"('{year:04d}-{month:02d}-{day:02d}',{i},'分量')")
        random.Random(8045).shuffle(components)
        mixed=[column('d'+str(i),'DATE' if i%2==0 else 'YEAR',True) for i in range(10)]
        mixed.insert(5,column('id','INT'))
        mixed+=[column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or (key==2 and i%2==0) else ("'2024-02-29'" if i%2==0 else '2024') for i in range(10)]
            vals.insert(5,str(key));vals+=["'日期😀'", "REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases=[('date_values',[column('id','INT'),column('value','DATE',True)],date_values),
               ('year_values',[column('id','INT'),column('value','YEAR',True)],year_values),
               ('date_components',[column('value','DATE'),column('id','INT'),column('note','VARCHAR',True,32)],components),
               ('date_mixed',mixed,rows)]
    if args.datetimes:
        def dt(name,fsp,nullable=True):
            c=column(name,'DATETIME',nullable);c['fsp']=fsp;return c
        def fraction(fsp,value):
            return ('.'+str(value).zfill(fsp)) if fsp else ''
        cases=[]
        for fsp in range(7):
            dates=[None,'0000-00-00 00:00:00','0001-01-01 00:00:00','1000-01-01 00:00:00','1900-02-29 01:02:03','2024-00-15 12:34:56','2024-05-00 12:34:56','2024-02-29 23:59:59','9999-12-31 23:59:59']
            values=['NULL' if d is None else "'"+d+fraction(fsp,0)+"'" for d in dates]
            for micro in [0,1 if fsp else 0,10**fsp-1,int('123456'[:fsp]) if fsp else 0,10**(fsp-1) if fsp else 0]:
                values.append("'2024-02-29 12:34:56"+fraction(fsp,micro)+"'")
            values.append("'2024-02-29 23:59:59.999999'")
            values.append("'9999-12-31 23:59:59"+fraction(fsp,10**fsp-1)+"'")
            cases.append(('datetime_fsp_'+str(fsp),[column('id','INT'),dt('value',fsp)],
                          [f'({i},{value})' for i,value in enumerate(values)]))
        mixed=[dt('d'+str(i),i%7) for i in range(9)];mixed.insert(4,column('id','INT'))
        mixed+=[column('day','DATE',True),column('year','YEAR',True),column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or key==2 and i%2==0 else "'2024-02-29 12:34:56"+fraction(i%7,int('123456'[:i%7]) if i%7 else 0)+"'" for i in range(9)]
            vals.insert(4,str(key));vals+=['NULL' if key==3 else "'2024-02-29'",'NULL' if key==3 else '2024',"'时间😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('datetime_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('datetime_tree',[dt('value',6),column('id','BIGINT'),dt('short_value',3),column('note','VARCHAR',True,100)],
                      [f"('2024-02-29 12:{(k//60)%60:02d}:{k%60:02d}.{k:06d}',{k},"+('NULL' if k%5==0 else f"'2024-02-29 12:34:56.{k:03d}'")+",REPEAT('界',100))" for k in keys]))
    if args.times:
        def tm(name,fsp):
            c=column(name,'TIME',True);c['fsp']=fsp;return c
        def frac(fsp,value):
            return '.'+str(value).zfill(fsp) if fsp else ''
        cases=[]
        for fsp in range(7):
            values=[None,'00:00:00','-00:00:00','00:00:01','-00:00:01','12:34:56','-12:34:56','25:00:00','-25:00:00','838:59:59','-838:59:59']
            values=[v+frac(fsp,0) if v is not None else None for v in values]
            for base in ['00:00:00','-00:00:00','12:34:56','-12:34:56','838:59:58','-838:59:58']:
                for tail in [1 if fsp else 0,int('123456'[:fsp]) if fsp else 0,10**fsp-1]:
                    values.append(base+frac(fsp,tail))
            values+=['00:00:59.999999','-00:00:59.999999']
            cases.append(('time_fsp_'+str(fsp),[column('id','INT'),tm('value',fsp)],
                          [f"({i},"+('NULL' if v is None else "'"+v+"'")+")" for i,v in enumerate(values)]))
        mixed=[tm('t'+str(i),i%7) for i in range(9)];mixed.insert(4,column('id','INT'))
        stamp=column('stamp','DATETIME',True);stamp['fsp']=6
        mixed+=[stamp,column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or key==2 and i%2==0 else "'-25:34:56"+frac(i%7,int('123456'[:i%7]) if i%7 else 0)+"'" for i in range(9)]
            vals.insert(4,str(key));vals+=['NULL' if key==3 else "'2024-02-29 12:34:56.123456'","'时长😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('time_mixed',mixed,rows))
        keys=list(range(-300,300));random.Random(8045).shuffle(keys)
        cases.append(('time_tree',[tm('value',6),column('id','BIGINT'),tm('short_value',3),column('note','VARCHAR',True,100)],
                      [f"('{'-' if k<0 else ''}{abs(k):02d}:34:56.{abs(k):06d}',{k},"+('NULL' if k%5==0 else f"'-00:00:00.{abs(k):03d}'")+",REPEAT('界',100))" for k in keys]))
    if args.timestamps:
        def ts(name,fsp,kind='TIMESTAMP'):
            c=column(name,kind,True);c['fsp']=fsp;return c
        def frac(fsp,value):
            return '.'+str(value).zfill(fsp) if fsp else ''
        cases=[]
        for fsp in range(7):
            values=[None,'0000-00-00 00:00:00','1970-01-01 00:00:01','2038-01-19 03:14:07','2024-02-29 12:34:56']
            values=[v+frac(fsp,0) if v is not None else None for v in values]
            values += ['2024-02-29 12:34:56'+frac(fsp,n) for n in [0,1 if fsp else 0,int('123456'[:fsp]) if fsp else 0,10**fsp-1,10**(fsp-1) if fsp else 0]]
            values += ['2024-02-29 23:59:59.999999','2038-01-19 03:14:07'+frac(fsp,10**fsp-1)]
            cases.append(('timestamp_fsp_'+str(fsp),[column('id','INT'),ts('value',fsp)],
                          [f"({i},"+('NULL' if v is None else "'"+v+"'")+")" for i,v in enumerate(values)]))
        mixed=[ts('t'+str(i),i%7) for i in range(9)];mixed.insert(4,column('id','INT'))
        mixed += [ts('wall',6,'DATETIME'),column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or key==2 and i%2==0 else "'2024-02-29 12:34:56"+frac(i%7,int('123456'[:i%7]) if i%7 else 0)+"'" for i in range(9)]
            vals.insert(4,str(key));vals += ['NULL' if key==3 else "'2024-02-29 12:34:56.123456'","'时间😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('timestamp_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('timestamp_tree',[ts('value',6),column('id','BIGINT'),ts('short_value',3),column('note','VARCHAR',True,100)],
                      [f"('2024-02-29 12:34:56.{k:06d}',{k},"+('NULL' if k%5==0 else f"'2024-02-29 12:34:56.{k:03d}'")+",REPEAT('界',100))" for k in keys]))
        zones=['+00:00','+08:00','-05:30']
        cases.append(('timestamp_zones',[column('id','INT'),ts('stamp',6),ts('wall',6,'DATETIME')],
                      [f"({i},'2024-02-29 12:34:56.123456','2024-02-29 12:34:56.123456')" for i in range(3)]))
    if args.bits:
        def bit(name,width):
            c=column(name,'BIT',True);c['bit_length']=width;return c
        columns=[column('id','INT')]+[bit('b'+str(n),n) for n in range(1,65)]
        rows=[]
        for key in range(69):
            values=[]
            for n in range(1,65):
                mask=(1<<n)-1
                v=None if key==0 else 0 if key==1 else mask if key==2 else 0xaaaaaaaaaaaaaaaa&mask if key==3 else 0x5555555555555555&mask if key==4 else (1<<(key-5))&mask
                values.append('NULL' if v is None else str(v))
            rows.append('('+','.join([str(key)]+values)+')')
        cases=[('bit_widths',columns,rows)]
        widths=[1,7,8,9,15,16,31,63,64]
        mixed=[bit('b'+str(i),n) for i,n in enumerate(widths)];mixed.insert(4,column('id','INT'))
        mixed += [column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            values=['NULL' if key==3 or key==2 and i%2==0 else str((1<<n)-1) for i,n in enumerate(widths)]
            values.insert(4,str(key));values += ["'位字段😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(values)+')')
        cases.append(('bit_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('bit_tree',[bit('value',64),column('id','BIGINT'),bit('short_value',9),column('note','VARCHAR',True,100)],
                      [f"({(1<<64)-1-k},{k},"+('NULL' if k%5==0 else str(k%512))+",REPEAT('界',100))" for k in keys]))
    if args.fixed_binary:
        def fixed(name,n):
            c=column(name,'BINARY',True);c['max_bytes']=n;return c
        def literal(b):
            return "X'"+b.hex()+"'"
        cases=[]
        for start in range(1,256,16):
            widths=list(range(start,min(start+16,256)))
            columns=[column('id','INT')]+[fixed('b'+str(n),n) for n in widths]
            rows=[]
            for key in range(6):
                values=[]
                for n in widths:
                    value=None if key==0 else b'' if key==1 else b'a' if key==2 else bytes((n+i)%256 for i in range(n)) if key==3 else b' '*n if key==4 else b'\xff\x00'[:n]
                    values.append('NULL' if value is None else literal(value))
                rows.append('('+','.join([str(key)]+values)+')')
            cases.append(('binary_widths_'+str(start),columns,rows))
        widths=[1,2,3,7,8,16,63,128,255]
        mixed=[fixed('b'+str(i),n) for i,n in enumerate(widths)];mixed.insert(4,column('id','INT'))
        varying=column('varying','VARBINARY',True);varying['max_bytes']=255
        mixed += [varying,column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            values=['NULL' if key==3 or key==2 and i%2==0 else literal((b'a' if key==0 else b'\x00\xff' if key==1 else b'')[:n]) for i,n in enumerate(widths)]
            values.insert(4,str(key));values += ["X''" if key in [0,3] else "X'00ff'" if key==1 else 'NULL',"'定长😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(values)+')')
        cases.append(('binary_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('binary_tree',[fixed('value',255),column('id','BIGINT'),fixed('short_value',3),column('note','VARCHAR',True,100)],
                      [f"({literal(k.to_bytes(2,'big'))},{k},"+('NULL' if k%5==0 else literal(b'\xff'))+",REPEAT('界',100))" for k in keys]))
    if args.enums:
        def enum(name,labels):
            c=column(name,'ENUM',True);c['enum_values']=labels;return c
        def quote(v):
            return "'"+v.replace("\\","\\\\").replace("'","''")+"'"
        cases=[]
        for count in [1,255,256,65535]:
            labels=['v'+str(i) for i in range(1,count+1)]
            indexes=[None]+list(range(count+1)) if count<=256 else [None,0,1,255,256,32767,32768,65535]
            cases.append(('enum_'+str(count),[column('id','INT'),enum('value',labels)],
                          [f"({i},"+('NULL' if v is None else str(v))+")" for i,v in enumerate(indexes)]))
        labels=['','ready','中文😀','2',"a'b",'slash\\path','a,b']
        cases.append(('enum_labels',[column('id','INT'),enum('value',labels)],
                      [f"({i},"+('NULL' if v is None else str(v))+")" for i,v in enumerate([None,0]+list(range(1,8))+["'2'"])]))
        mixed=[enum('e'+str(i),labels if i%2==0 else ['v'+str(n) for n in range(1,257)]) for i in range(9)];mixed.insert(4,column('id','INT'))
        mixed += [column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            values=['NULL' if key==3 or key==2 and i%2==0 else str(0 if key==0 else 3 if i%2==0 else 256) for i in range(9)]
            values.insert(4,str(key));values += ["'枚举😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(values)+')')
        cases.append(('enum_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('enum_tree',[enum('value',['v'+str(i) for i in range(1,257)]),column('id','BIGINT'),enum('short_value',labels),column('note','VARCHAR',True,100)],
                      [f"({k%257},{k},"+('NULL' if k%5==0 else str(k%8))+",REPEAT('界',100))" for k in keys]))
    if args.sets:
        def st(name,labels):
            c=column(name,'SET',True);c['set_values']=labels;return c
        def quote(v):
            return "'"+v.replace("\\","\\\\").replace("'","''")+"'"
        columns=[column('id','INT')]+[st('s'+str(n),['v'+str(i) for i in range(n)]) for n in range(1,65)]
        rows=[]
        for key in range(69):
            vals=[]
            for n in range(1,65):
                mask=(1<<n)-1
                value=None if key==0 else 0 if key==1 else mask if key==2 else 0xaaaaaaaaaaaaaaaa&mask if key==3 else 0x5555555555555555&mask if key==4 else (1<<(key-5))&mask
                vals.append('NULL' if value is None else str(value))
            rows.append('('+','.join([str(key)]+vals)+')')
        cases=[('set_widths',columns,rows)]
        labels=[['','a','b'],['a','','b'],['a','b',''],['中文😀','ready','2',"a'b",'slash\\path']]
        rows=[f"({i},"+','.join(['NULL' if value is None else str(value)]*4)+')' for i,value in enumerate([None]+list(range(8)))]
        rows += ["(9,7,7,7,31)","(10,'b,a,a','b,a,a','b,a,a','ready,中文😀,ready')","(11,'','','','')"]
        cases.append(('set_labels',[column('id','INT')]+[st('s'+str(i),v) for i,v in enumerate(labels)],rows))
        counts=[1,8,9,16,17,24,25,33,64]
        mixed=[st('s'+str(i),['v'+str(n) for n in range(count)]) for i,count in enumerate(counts)];mixed.insert(4,column('id','INT'))
        e=column('state','ENUM',True);e['enum_values']=['','ready','中文😀']
        mixed += [e,column('note','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or key==2 and i%2==0 else str(0 if key==0 else (1<<n)-1) for i,n in enumerate(counts)]
            vals.insert(4,str(key));vals += ['NULL' if key==3 else '3',"'集合😀'","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('set_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('set_tree',[st('value',['v'+str(i) for i in range(64)]),column('id','BIGINT'),st('short_value',['a','b','c']),column('note','VARCHAR',True,100)],
                      [f"({(1<<63)|k},{k},"+('NULL' if k%5==0 else str(k%8))+",REPEAT('界',100))" for k in keys]))
    if args.chars:
        def ch(name,n):
            return column(name,'CHAR',True,n)
        def literal(text):
            return "CONVERT(X'"+text.encode().hex()+"' USING utf8mb4)"
        cases=[]
        for start in range(1,256,8):
            widths=list(range(start,min(start+8,256)))
            cols=[column('id','INT')]+[ch('c'+str(n),n) for n in widths]
            rows=[]
            for key in range(10):
                vals=[]
                for n in widths:
                    text=[None,'','a','界'*n,'😀'*n,' '*n,'\u00a0'*n,'\t','\0','a ' if n>1 else ' '][key]
                    vals.append('NULL' if text is None else literal(text))
                rows.append('('+','.join([str(key)]+vals)+')')
            cases.append(('char_widths_'+str(start),cols,rows))
        widths=[1,3,5,32,63,64,127,192,255]
        mixed=[ch('c'+str(i),n) for i,n in enumerate(widths)];mixed.insert(4,column('id','INT'))
        mixed += [column('varying','VARCHAR',True,32),column('body','TEXT',True)]
        rows=[]
        for key in range(4):
            vals=['NULL' if key==3 or key==2 and i%2==0 else literal('a' if key==0 else '界' if key==1 else '') for i in range(9)]
            vals.insert(4,str(key));vals += ["' a  '","REPEAT('界',20000)" if key==1 else ('NULL' if key==3 else "''")]
            rows.append('('+','.join(vals)+')')
        cases.append(('char_mixed',mixed,rows))
        keys=list(range(600));random.Random(8045).shuffle(keys)
        cases.append(('char_tree',[ch('value',255),column('id','BIGINT'),ch('short_value',3),column('note','VARCHAR',True,100)],
                      [f"('行{k}',{k},"+('NULL' if k%5==0 else "'界 '")+",REPEAT('界',100))" for k in keys]))
        cases.append(('char_external',[column('id','INT')]+[ch('c'+str(i),255) for i in range(12)],
                      ["(0,"+','.join(["REPEAT('😀',255)"]*12)+')',"(1,"+','.join(['NULL']*12)+')']))
    compressed = args.tree or args.integers or args.variable or args.lob or args.large_lob or args.decimal or args.floating or args.dates or args.datetimes or args.times or args.timestamps or args.bits or args.fixed_binary or args.enums or args.sets or args.chars
    try:
        env = json.loads(query("SELECT JSON_OBJECT('version',VERSION(),'page_size',"
                               "@@innodb_page_size,'file_per_table',@@innodb_file_per_table,"
                               "'datadir',@@datadir,'checksum',@@innodb_checksum_algorithm)")[0])
        if env['version'] != '8.0.45' or env['page_size'] != 16384 or not env['file_per_table']:
            raise RuntimeError('fixtures require MySQL 8.0.45, 16 KiB, file_per_table=ON')
        sql = ['CREATE DATABASE `' + db + '`', 'USE `' + db + '`']
        if args.dates or args.datetimes:
            sql.insert(0,"SET SESSION sql_mode='ALLOW_INVALID_DATES'")
        if args.times or args.timestamps or args.bits or args.fixed_binary or args.enums or args.sets or args.chars:
            sql.insert(0,"SET SESSION sql_mode='STRICT_TRANS_TABLES'")
        if args.timestamps:
            sql.insert(0,"SET SESSION time_zone='+00:00'")
        if args.enums:
            sql.append("SET SESSION sql_mode=''")
        for name, columns, values in cases:
            fields = [f"`{c['name']}` " + (f"{c['type']}({c['max_chars']})" if c['type'] in ['CHAR','VARCHAR']
                       else "SET("+','.join(quote(v) for v in c['set_values'])+")" if c['type']=='SET'
                       else "ENUM("+','.join(quote(v) for v in c['enum_values'])+")" if c['type']=='ENUM'
                       else f"BIT({c['bit_length']})" if c['type'] == 'BIT'
                       else f"{c['type']}({c['max_bytes']})" if c['type'] in ['BINARY','VARBINARY']
                       else f"DECIMAL({c['precision']},{c['scale']})" + (' UNSIGNED' if c.get('unsigned') else '') if c['type']=='DECIMAL'
                       else f"{c['type']}({c['fsp']})" if c['type'] in ['DATETIME','TIME','TIMESTAMP']
                       else c['type'] + (' UNSIGNED' if c.get('unsigned') else '')) + (' NULL' if c['nullable'] else ' NOT NULL') for c in columns]
            sql.append(f"CREATE TABLE `{name}` (" + ', '.join(fields) +
                       ", PRIMARY KEY (`id`)) ENGINE=InnoDB ROW_FORMAT=DYNAMIC "
                       "DEFAULT CHARSET=utf8mb4 ENCRYPTION='N'")
            if args.timestamps and name=='timestamp_zones':
                for zone,value in zip(zones,values):
                    sql += [f"SET SESSION time_zone='{zone}'",f'INSERT INTO `{name}` VALUES '+value]
                sql.append("SET SESSION time_zone='+00:00'")
                continue
            if values:
                if args.integers or args.variable or args.lob or args.large_lob or args.decimal or args.floating or args.dates or args.datetimes or args.times or args.timestamps or args.bits or args.fixed_binary or args.enums or args.sets or args.chars:
                    for start in range(0,len(values),100):
                        sql.append(f'INSERT INTO `{name}` VALUES '+','.join(values[start:start+100]))
                elif args.tree:
                    for start in range(0,len(values),250):
                        tuples=[]
                        for key in values[start:start+250]:
                            fields = ([str(key)] + ["REPEAT('😀',63)"]*8 if name=='deep_rows'
                                      else [str(key), 'NULL' if key%7==0 else str(-key),
                                            'NULL' if key%11==0 else "'行"+str(key)+"'"])
                            tuples.append('('+','.join(fields)+')')
                        sql.append(f'INSERT INTO `{name}` VALUES '+','.join(tuples))
                else:
                    sql.append(f'INSERT INTO `{name}` VALUES ' + values)
        sql_text=';\n'.join(sql) + ';\n'
        if compressed:
            (out/'generate.sql.gz').write_bytes(gzip.compress(sql_text.encode(),mtime=0))
        else:
            (out / 'generate.sql').write_text(sql_text, encoding='utf-8')
        for statement in sql:
            query(statement)
        if args.dates or args.datetimes or args.times or args.timestamps or args.bits or args.fixed_binary or args.enums or args.sets or args.chars:
            env['sql_mode']=query('SELECT @@SESSION.sql_mode')[0]
        if args.timestamps:
            env['time_zone']=query('SELECT @@SESSION.time_zone')[0]
        table_list = ', '.join('`' + n + '`' for n, _, _ in cases)
        query('FLUSH TABLES ' + table_list + ' FOR EXPORT')
        manifest = dict(database=db, generated_at=datetime.now(timezone.utc).isoformat(),
                        environment=env, snapshot='Same session: FOR EXPORT, SELECT, copy, UNLOCK',
                        cases=[])
        for name, columns, _ in cases:
            index = json.loads(query("SELECT JSON_OBJECT('root_page',i.PAGE_NO,'index_id',"
                                     "i.INDEX_ID,'space_id',i.SPACE) FROM "
                                     "INFORMATION_SCHEMA.INNODB_INDEXES i JOIN "
                                     "INFORMATION_SCHEMA.INNODB_TABLES t ON i.TABLE_ID=t.TABLE_ID "
                                     f"WHERE t.NAME='{db}/{name}' AND i.NAME='PRIMARY'")[0])
            schema = dict(columns=columns, primary_key='id', **index)
            select_sql = ('SELECT JSON_ARRAY(' +
                        ','.join(('CAST(`'+c['name']+'` AS UNSIGNED)' if c['type']=='BIT' else 'HEX(`'+c['name']+'`)' if c['type'] in ['BINARY','VARBINARY'] or c['type'].endswith('BLOB') else 'CAST(`'+c['name']+'` AS CHAR)' if c['type'] in ['DECIMAL','DATE','DATETIME','TIME','TIMESTAMP','ENUM','SET','CHAR'] else '`'+c['name']+'`') for c in columns) + f') FROM `{name}` ORDER BY id')
            expected = [json.loads(x) for x in query(select_sql)]
            if args.timestamps and name=='timestamp_zones':
                displays={}
                for zone in zones:
                    query(f"SET SESSION time_zone='{zone}'")
                    displays[zone]=[json.loads(x) for x in query(select_sql)]
                query("SET SESSION time_zone='+00:00'")
                (out/(name+'.zones.json.gz')).write_bytes(gzip.compress(json.dumps(displays,ensure_ascii=False,indent=2).encode(),mtime=0))
            if args.enums or args.sets:
                enum_columns=[c for c in columns if c['type']=='ENUM']
                ordinal_sql='SELECT JSON_ARRAY('+','.join('CAST(`'+c['name']+'` AS UNSIGNED)' for c in enum_columns)+f') FROM `{name}` ORDER BY id'
                ordinals=[json.loads(x) for x in query(ordinal_sql)]
                (out/(name+'.ordinals.json.gz')).write_bytes(gzip.compress(json.dumps(ordinals,indent=2).encode(),mtime=0))
            if args.sets:
                mask_sql='SELECT JSON_ARRAY('+','.join('CAST(`'+c['name']+'` AS UNSIGNED)' for c in columns if c['type']=='SET')+f') FROM `{name}` ORDER BY id'
                masks=[json.loads(x) for x in query(mask_sql)]
                (out/(name+'.masks.json.gz')).write_bytes(gzip.compress(json.dumps(masks,indent=2).encode(),mtime=0))
            ddl = '\n'.join(query('SHOW CREATE TABLE `' + name + '`')).split('\t', 1)[1]
            (out / (name + '.sql')).write_text(ddl + ';\n', encoding='utf-8')
            (out / (name + '.json')).write_text(json.dumps(schema, ensure_ascii=False, indent=2)+'\n')
            expected_text=json.dumps(expected, ensure_ascii=False, indent=2)+'\n'
            if compressed:
                (out/(name+'.expected.json.gz')).write_bytes(gzip.compress(expected_text.encode(),mtime=0))
            else:
                (out / (name + '.expected.json')).write_text(expected_text)
            src = Path(env['datadir']) / db / (name + '.ibd')
            dest = out / (name + '.ibd')
            shutil.copyfile(src, dest)
            data = dest.read_bytes()
            root = index['root_page'] * 16384
            level=int.from_bytes(data[root+64:root+66], 'big')
            required_level = (1 if name=='variable_tree' else 0) if args.variable else (2 if name=='deep_rows' else (1 if compressed else 0))
            if args.lob:
                required_level=1 if name=='lob_mixed_tree' else 0
            if args.large_lob:
                required_level=0
            if args.decimal:
                required_level=1 if name=='decimal_tree' else level
            if args.floating:
                required_level=1 if name=='float_tree' else 0
            if args.dates:
                required_level=1 if name=='date_components' else 0
            if args.datetimes:
                required_level=1 if name=='datetime_tree' else 0
            if args.times:
                required_level=1 if name=='time_tree' else 0
            if args.timestamps:
                required_level=1 if name=='timestamp_tree' else 0
            if args.bits:
                required_level=1 if name in ['bit_tree','bit_widths'] else 0
            if args.fixed_binary:
                required_level=1 if name=='binary_tree' else level if name.startswith('binary_widths_') else 0
            if args.enums:
                required_level=1 if name=='enum_tree' else 0
            if args.sets:
                required_level=1 if name in ['set_widths','set_tree'] else 0
            if args.chars:
                required_level=1 if name=='char_tree' else level if name.startswith('char_widths_') else 0
            if level!=required_level:
                raise RuntimeError(f'fixture root level {level}, expected {required_level}')
            if compressed:
                (out/(name+'.ibd.gz')).write_bytes(gzip.compress(data,mtime=0))
                dest.unlink()
            manifest['cases'].append(dict(name=name, rows=len(expected), **({'expected_error':'value_limit'} if name=='long_over_limit' else {}), **({'expected_error':'unsupported_external'} if args.variable and name.startswith('external_') else {}), **index,
                                          root_level=level, bytes=len(data), sha256=hashlib.sha256(data).hexdigest()))
        query('UNLOCK TABLES')
        (out / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(dict(output=str(out), database=db, cases=len(cases)), ensure_ascii=False))
    finally:
        # Connection close also releases export locks on error. The unique database remains.
        proc.stdin.close()
        proc.wait(timeout=15)
        proc.stdout.close()


if __name__ == '__main__':
    main()
