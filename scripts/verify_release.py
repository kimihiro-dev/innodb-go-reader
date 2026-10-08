#!/usr/bin/env python3
"""Verify the frozen local release using existing tests; never contact MySQL.
Run from any directory. --out must name a new directory; failures retain logs
and a report with complete=false. Go's test cache is permitted and visible.
"""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]


def fingerprint(paths):
    digest = hashlib.sha256()
    for path in sorted(paths):
        digest.update(path.relative_to(ROOT).as_posix().encode() + b'\0')
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return {'files': len(paths), 'sha256': digest.hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, help='new directory for logs/report.json')
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=False)
    sources = list(ROOT.glob('*.go'))
    for folder in ('cmd', 'internal', 'rowio', 'visual', 'examples'):
        sources += list((ROOT / folder).rglob('*.go'))
    sources += [ROOT / 'go.mod', ROOT / 'visual/report.html', Path(__file__).resolve()]
    fixtures = sorted(p for p in (ROOT / 'testdata').rglob('*') if p.is_file())
    report = {'baseline': '2026-09-30', 'complete': False, 'checks': [],
              'source': fingerprint(sources), 'testdata': fingerprint(fixtures),
              'fixture_counts': {key: len(list((ROOT / 'testdata').glob(pattern)))
                                 for key, pattern in [('row', '*/*.ibd*'),
                                                      ('space', 'space/*.space.gz'),
                                                      ('partition', 'partitions/*.partition.gz')]}}

    def run(name, command, expected=0, env=None):
        print(name, flush=True)
        started = time.monotonic()
        with (out / (name + '.log')).open('wb') as log:
            proc = subprocess.run(command, cwd=ROOT, stdout=log,
                                  stderr=subprocess.STDOUT, env=env)
        report['checks'].append({'name': name, 'exit_code': proc.returncode,
                                 'expected_exit_code': expected,
                                 'seconds': round(time.monotonic() - started, 3),
                                 'log': name + '.log'})
        if proc.returncode != expected:
            raise RuntimeError(f'{name}: exit {proc.returncode}, expected {expected}')

    try:
        if report['fixture_counts'] != {'row': 487, 'space': 4, 'partition': 34}:
            raise RuntimeError('frozen fixture counts changed; review support baseline')
        run('environment', ['go', 'version'])
        run('platform', ['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED'])
        run('race', ['go', 'test', '-race', '-cover', '-timeout=25m', './...'])
        run('vet', ['go', 'vet', './...'])
        for package, target in [('.', 'FuzzRead'), ('.', 'FuzzPartitionDirectory'),
                                ('./rowio', 'FuzzDecoder'), ('./visual', 'FuzzHTMLReport')]:
            run(target, ['go', 'test', package, '-run', '^$', '-fuzz', '^' + target + '$',
                         '-fuzztime=10s', '-parallel=2'])
        run('benchmark', ['go', 'test', '-run', '^$', '-bench', '^BenchmarkTableScan$',
                          '-benchtime=3x', '-benchmem'])
        run('memory', ['go', 'test', '-run', '^TestScanMemory$', '-count=1', '-v'],
            env=dict(os.environ, INNODB_SCAN_MEMORY='1'))
        with tempfile.TemporaryDirectory(prefix='innodb-release-') as temporary:
            tmp = Path(temporary)
            binary = tmp / 'innodb-reader'
            run('build', ['go', 'build', '-o', str(binary), './cmd/innodb-reader'])
            lesson = ROOT / 'testdata/mysql8045/lesson_rows.ibd'
            run('check-rows', [str(binary), 'check', str(lesson)])
            row_report = json.loads((out / 'check-rows.log').read_text().splitlines()[0])
            if row_report.get('Records') != 4 or not row_report.get('Complete'):
                raise RuntimeError('lesson must contain four complete rows')
            run('export', [str(binary), 'export', '--output', str(tmp / 'rows.jsonl'), str(lesson)])
            run('visualize', [str(binary), 'visualize', '--index', 'PRIMARY', '--pages', '0,4',
                              '--output', str(tmp / 'lesson.html'), str(lesson)])
            virtual = tmp / 'virtual.ibd'
            virtual.write_bytes(gzip.decompress((ROOT / 'testdata/generated/mixed_instant.ibd.gz').read_bytes()))
            target = tmp / 'protected.jsonl'
            target.write_bytes(b'preserved\n')
            run('reject-virtual', [str(binary), 'export', '--overwrite', '--output', str(target),
                                   str(virtual)], expected=1)
            if target.read_bytes() != b'preserved\n':
                raise RuntimeError('failed export changed existing output')
            run('materialized', [str(binary), 'check', '--materialized', str(virtual)])
            damaged = tmp / 'damaged.ibd'
            data = bytearray(lesson.read_bytes())
            data[4 * 16384 + 200] ^= 1
            damaged.write_bytes(data)
            run('reject-crc', [str(binary), 'check', str(damaged)], expected=1)
            run('reject-page', [str(binary), 'visualize', '--pages', '999', str(lesson)], expected=1)
        if fingerprint(sources) != report['source'] or fingerprint(fixtures) != report['testdata']:
            raise RuntimeError('source/testdata changed during verification')
        report['complete'] = True
    finally:
        (out / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print('complete:', out / 'report.json', flush=True)


if __name__ == '__main__':
    main()
