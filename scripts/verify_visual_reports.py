#!/usr/bin/env python3
"""Generate offline HTML examples and independently verify embedded source bytes/API space data.
This checks HTML data and JavaScript syntax, not browser rendering or interactions.
"""
import argparse,gzip,json,os,re,subprocess,tempfile
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--out',required=True,help='new directory for example reports and verification.json')
a=p.parse_args();out=Path(a.out);out.mkdir(parents=True,exist_ok=False)
env=os.environ.copy();env.setdefault('GOCACHE','/private/tmp/innodb-go-reader-stage30-cache')

def exact(v):
    if isinstance(v,bool) or v is None:return v
    if isinstance(v,int):return str(v)
    if isinstance(v,list):return [exact(x) for x in v]
    if isinstance(v,dict):return {k:exact(x) for k,x in v.items()}
    return v

def embedded(text):
    m=re.search(r'<script id="report-data" type="application/json">(.*?)</script>',text,re.S)
    assert m
    return json.loads(m[1])

cases=[('lesson','mysql8045/lesson_rows.ibd','PRIMARY',False),('lob','variable/external_text.ibd.gz','PRIMARY',False),('secondary','secondary/deep.ibd.gz','b_idx',False),('virtual','generated/mixed_instant.ibd.gz','PRIMARY',True),('space','space/grown.space.gz','',False),('partition-space','partitions/ranges-p0.partition.gz','',False)]
checks=[]
with tempfile.TemporaryDirectory(prefix='innodb-visual-check-') as temporary:
    tmp=Path(temporary);binary=tmp/'reader'
    subprocess.run(['go','build','-o',str(binary),'./cmd/innodb-reader'],env=env,check=True)
    for name,source,index,materialized in cases:
        b=(Path('testdata')/source).read_bytes()
        if source.endswith('.gz'):b=gzip.decompress(b)
        path=tmp/(name+'.ibd');path.write_bytes(b)
        flags=['--index',index] if index else []
        if materialized:flags+=['--materialized']
        def run(pages):
            selected=['--pages',','.join(map(str,pages))] if pages else []
            proc=subprocess.run([str(binary),'visualize',*flags,*selected,str(path)],capture_output=True,text=True)
            assert proc.returncode==0,proc.stderr
            return proc.stdout,embedded(proc.stdout)
        _,first=run([])
        selected=[0]
        if index:
            selected.append(int(first['IndexRoot']))
            leaves=[int(v['Number']) for v in first['TreePages'] if v['Level']=='0']
            if leaves:selected.append(leaves[0])
        html,report=run(sorted(set(selected)))
        for record in report['Details'] or []:
            for f in record.get('External',[]):
                selected += [int(f['FirstPage'])]+[int(c['PageNumber']) for c in f['Chunks'][:2]]
        selected=sorted(set(selected));html,report=run(selected)
        expected=json.loads(subprocess.check_output([str(binary),'space',str(path)],stderr=subprocess.DEVNULL,text=True))
        for page in expected['Pages']:page.pop('Raw',None)
        assert report['Space']==exact(expected)
        assert [int(v['Number']) for v in report['RawPages']]==selected
        for raw in report['RawPages']:
            number=int(raw['Number']);assert raw['Hex']==b[number*16384:(number+1)*16384].hex()
        for detail in report['Details'] or []:
            assert 0<=int(detail['Start'])<=int(detail['Origin'])<int(detail['End'])<=16384
            for f in detail.get('External',[]):
                start=int(detail['Page'])*16384+int(f['Offset'])
                assert bytes(map(int,f['Reference']))==b[start:start+20]
        assert html.count('</script>')==2 and 'ZgotmplZ' not in html
        js=re.findall(r'<script>(.*?)</script>',html,re.S);assert len(js)==1
        script=tmp/'report.js';script.write_text(js[0]);subprocess.run(['node','--check',str(script)],check=True)
        target=out/(name+'.html');target.write_text(html)
        checks.append({'name':name,'source':source,'pages':len(report['Space']['Pages']),'tree_pages':len(report['TreePages'] or []),'edges':len(report['Edges'] or []),'details':len(report['Details'] or []),'embedded_pages':selected,'html_bytes':target.stat().st_size,'api_space_equal':True,'raw_equal':True,'script_syntax':True})
        print(name,checks[-1],flush=True)
(out/'verification.json').write_text(json.dumps({'browser_interaction':'not verified: file URL rejected by browser policy','cases':checks},ensure_ascii=False,indent=2)+'\n')
