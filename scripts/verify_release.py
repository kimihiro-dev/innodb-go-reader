#!/usr/bin/env python3
"""Verify the current library using existing tests; never contact MySQL.
Run from any directory. --out must name a new directory; failures retain logs
and a report with complete=false. Go's test cache is permitted and visible.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
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
    for folder in ('rowio', 'stats', 'examples'):
        sources += list((ROOT / folder).rglob('*.go'))
    sources += [ROOT / 'go.mod', Path(__file__).resolve()]
    fixtures = sorted(p for p in (ROOT / 'testdata').rglob('*') if p.is_file())
    report = {'working_tree': True, 'complete': False, 'checks': [],
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
        run('environment', ['go', 'version'])
        run('platform', ['go', 'env', 'GOOS', 'GOARCH', 'CGO_ENABLED'])
        run('race', ['go', 'test', '-race', '-cover', '-timeout=25m', './...'])
        run('vet', ['go', 'vet', './...'])
        for package, target in [('.', 'FuzzRead'), ('.', 'FuzzPartitionDirectory'),
                                ('./rowio', 'FuzzDecoder')]:
            run(target, ['go', 'test', package, '-run', '^$', '-fuzz', '^' + target + '$',
                         '-fuzztime=10s', '-parallel=2'])
        run('benchmark', ['go', 'test', '-run', '^$', '-bench', '^BenchmarkTableScan$',
                          '-benchtime=3x', '-benchmem'])
        run('memory', ['go', 'test', '-run', '^TestScanMemory$', '-count=1', '-v'],
            env=dict(os.environ, INNODB_SCAN_MEMORY='1'))
        if fingerprint(sources) != report['source'] or fingerprint(fixtures) != report['testdata']:
            raise RuntimeError('source/testdata changed during verification')
        report['complete'] = True
    finally:
        (out / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print('complete:', out / 'report.json', flush=True)


if __name__ == '__main__':
    main()
