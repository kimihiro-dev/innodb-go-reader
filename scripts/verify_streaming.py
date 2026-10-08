#!/usr/bin/env python3
"""Offline stage-32 CLI protocol checks; uses existing immutable snapshots."""
import base64
import decimal
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

env = dict(os.environ)
env.setdefault('GOCACHE', '/private/tmp/innodb-go-reader-stage32-cache')
cases = [
    'mysql8045/lesson_rows.ibd',
    'cluster/rowid_deep.ibd.gz',
    'lob_updates/documents_repeated.ibd.gz',
    'instant/lesson_readd.ibd.gz',
    'generated/tree_instant.ibd.gz',
    'compact/lesson_compact_initial.ibd.gz',
    'compact-legacy/lesson_compact_mixed_formats.ibd.gz',
    'large_lob/long_over_limit.ibd.gz',
    'generated/functional_rejected.ibd.gz',
]
def decode(raw):
    return json.loads(raw, parse_float=decimal.Decimal)

with tempfile.TemporaryDirectory(prefix='innodb-stream-verify-') as work:
    work = Path(work)
    for example in ('scan', 'streamlob', 'materialized'):
        subprocess.run(['go', 'build', '-o', str(work/example), './examples/'+example], env=env, check=True)
    total_rows = 0
    for case in cases:
        data = (Path('testdata')/case).read_bytes()
        if case.endswith('.gz'):
            data = gzip.decompress(data)
        file = work/'snapshot.ibd'
        file.write_bytes(data)
        atomic = subprocess.run([str(work/'materialized'), str(file)], capture_output=True)
        streamed = subprocess.run([str(work/'scan'), '-materialized', str(file)], capture_output=True)
        lines = [decode(x) for x in streamed.stdout.splitlines()]
        final = lines[-1]
        if atomic.returncode:
            assert streamed.returncode and not final['report']['Complete'] and final['error'], case
            continue
        assert not streamed.returncode and final['report']['Complete'], (case, streamed.stderr)
        report = decode(atomic.stdout)
        events = [x['event'] for x in lines[:-1]]
        for name, result_key in [('Page','Pages'),('Node','Nodes'),('Record','Records'),('DeletedRecord','DeletedRecords')]:
            actual = [e[name] for e in events if e[name] is not None]
            assert actual == (report['Result'][result_key] or []), (case, result_key)
        assert final['report']['Columns'] == report['Columns']
        assert final['report']['VirtualColumns'] == report['VirtualColumns']
        total_rows += final['report']['Records']
        limited = subprocess.run([str(work/'scan'), '-materialized', '-max-rows', '1', str(file)], capture_output=True)
        end = decode(limited.stdout.splitlines()[-1])
        assert limited.returncode and not end['report']['Complete'] and end['report']['Records'] == 1, case
        # Exercise the raw CLI with both prefix and actual old/new references.
        if case.startswith(('compact/', 'compact-legacy/')):
            source = report['Result']['Records'][0]['External'][0]
            ref = bytes(source['Reference']).hex()
            prefix = base64.b64decode(source['Prefix']).hex()
            raw = subprocess.run([str(work/'streamlob'), str(file), ref, prefix], capture_output=True)
            status = decode(raw.stderr)
            text = report['Result']['Records'][0]['Values'][source['Column']].encode('utf-8')
            assert not raw.returncode and status['Complete'] and raw.stdout == text, case
    # Over-limit raw CLI: obtain trusted source bytes from the known fixture record,
    # without using the typed reader which correctly refuses materialization.
    data = gzip.decompress(Path('testdata/large_lob/long_over_limit.ibd.gz').read_bytes())
    # Saved fixture root page has one record. Its next from infimum locates the origin;
    # INT key (4) + transaction (6) + roll pointer (7) precede the 20-byte reference.
    root = 4*16384
    origin = (99 + int.from_bytes(data[root+97:root+99], 'big', signed=True)) % 65536
    ref = data[root+origin+17:root+origin+37]
    assert len(ref) == 20 and int.from_bytes(ref[16:], 'big') == 16777217
    file = work/'over.ibd'; file.write_bytes(data)
    raw = subprocess.run([str(work/'streamlob'), str(file), ref.hex()], capture_output=True)
    expected = json.loads(gzip.decompress(Path('testdata/large_lob/long_over_limit.expected.json.gz').read_bytes()))
    sql = bytes.fromhex(expected[0][1])
    assert not raw.returncode and decode(raw.stderr)['Complete']
    assert len(raw.stdout) == len(sql) and hashlib.sha256(raw.stdout).digest() == hashlib.sha256(sql).digest()
    print(f'{len(cases)} snapshots; {total_rows} typed rows; complete/error envelopes, row budgets and raw LOB CLI passed')
