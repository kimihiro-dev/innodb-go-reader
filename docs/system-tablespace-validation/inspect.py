#!/usr/bin/env python3
"""Read fixed-offset tutorial observations; not a system-tablespace parser."""
import gzip
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PAGE = 16384

def integer(data, offset, length):
    return int.from_bytes(data[offset:offset+length], 'big')

def header(data, n):
    p = data[n*PAGE:(n+1)*PAGE]
    return dict(physical_page=n, header_page=integer(p,4,4), type=integer(p,24,2), space=integer(p,34,4),
                lsn=integer(p,16,8), first_38_hex=p[:38].hex(), payload_38_94_hex=p[38:94].hex())

out = {}
for version in ('5.7.44','8.0.45'):
    folder = ROOT/version
    source = json.loads((folder/'source.json').read_text())
    blobs = {}
    for f in source['files']:
        b = gzip.decompress((folder/f['artifact']).read_bytes())
        assert len(b)==f['bytes'] and hashlib.sha256(b).hexdigest()==f['sha256']
        blobs[f['source']]=b
    system = blobs['ibdata1']
    assert integer(system,34,4)==0 and integer(system,38,4)==0
    p7=system[7*PAGE:8*PAGE]
    counters={name:integer(p7,38+offset,width) for name,offset,width in
              [('row_id',0,8),('table_id',8,8),('index_id',16,8),('max_space_id',24,4)]}
    roots={name:integer(p7,38+offset,4) for name,offset in [('SYS_TABLES',32),('SYS_TABLE_IDS',36),('SYS_COLUMNS',40),('SYS_INDEXES',44),('SYS_FIELDS',48)]}
    p5=system[5*PAGE:6*PAGE]
    slots=[dict(slot=i,space=integer(p5,56+i*8,4),page=integer(p5,60+i*8,4)) for i in range(128) if integer(p5,60+i*8,4)!=0xffffffff]
    root_headers=[]
    if version.startswith('5.'):
        for name,n in roots.items():
            h=header(system,n);p=system[n*PAGE:(n+1)*PAGE]
            h.update(name=name,index_id=integer(p,66,8),level=integer(p,64,2),compact=bool(integer(p,42,2)&0x8000),root_header_38_94_hex=p[38:94].hex())
            root_headers.append(h)
        for sql in source['dictionary_roots_sql']:
            if sql['name']=='CLUST_IND':
                assert any(x['index_id']==sql['id'] and x['physical_page']==sql['root'] for x in root_headers)
    declared=[]
    for index in source['indexes']:
        data=system if index['space']==0 else blobs['lesson/private_rows.ibd']
        p=data[index['root']*PAGE:(index['root']+1)*PAGE]
        assert integer(p,66,8)==index['id'] and integer(p,34,4)==index['space']
        declared.append(dict(sql=index,observed=header(data,index['root'])))
    item=dict(system_pages=len(system)//PAGE,flags=integer(system,54,4),fsp_size=integer(system,46,4),
              free_limit=integer(system,50,4),pages_0_to_7=[header(system,n) for n in range(8)],
              dictionary_counters=counters,dictionary_roots_or_legacy_fields=roots,dictionary_root_headers=root_headers,
              trx_id_store=integer(p5,38,8),trx_slot_0_hex=p5[56:64].hex(),nonempty_legacy_trx_slots=slots,
              doublewrite_legacy_16184_16222_hex=p5[16184:16222].hex(),declared_indexes=declared)
    if version.startswith('5.'):
        start=integer(p5,16198,4);second=integer(p5,16202,4)
        assert integer(p5,16194,4)==536853855
        item['doublewrite_blocks']=[start,second]
        item['doublewrite_copy_headers']=[header(system,n) for n in range(start,start+6)]
    else:
        item['other_files']={}
        for name in ('mysql.ibd','undo_001','undo_002'):
            data=blobs[name]
            observation=dict(pages=len(data)//PAGE,flags=integer(data,54,4),page0=header(data,0))
            if name.startswith('undo_'):
                p=data[3*PAGE:4*PAGE]
                assert integer(p,24,2)==21 and integer(p,38,4)==0x52534548
                slots = [integer(p,n,4) for n in range(56,PAGE-200-8,4)]
                observation.update(rseg_array=header(data,3),array_size=integer(p,42,4),first_rseg=integer(p,56,4),nonempty_slots=len([n for n in slots if n!=0xffffffff]),array_header_38_72_hex=p[38:72].hex())
            item['other_files'][name]=observation
    if version=='8.0.45':
        item['sdi_headers'] = {}
        for name in ('ibdata1','mysql.ibd','lesson/private_rows.ibd'):
            data=blobs[name];root=integer(data,10509,4)
            assert integer(data,10505,4)==1 and integer(data,root*PAGE+24,2)==17853
            item['sdi_headers'][name]=dict(version=1,root=root,locator_10505_10513_hex=data[10505:10513].hex(),root_header=header(data,root))
    out[version]=item
(ROOT/'observations.json').write_text(json.dumps(out,indent=2)+'\n')
print('Verified hashes, fixed page fields, SQL index roots and 8.0 undo array headers')
