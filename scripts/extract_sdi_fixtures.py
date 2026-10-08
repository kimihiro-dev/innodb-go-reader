#!/usr/bin/env python3
"""Export official SDI expectations from existing immutable .ibd fixtures; no MySQL connection."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ibd2sdi', required=True)
    parser.add_argument('--out', required=True, help='New output directory')
    args = parser.parse_args()
    out = Path(args.out)
    if out.exists():
        parser.error('output directory already exists')
    sources = sorted(Path('testdata').rglob('*.ibd')) + sorted(Path('testdata').rglob('*.ibd.gz'))
    out.mkdir(parents=True)
    cases = []
    with tempfile.TemporaryDirectory(prefix='innodb-sdi-') as directory:
        snapshot = Path(directory) / 'table.ibd'
        for source in sources:
            data = source.read_bytes()
            if source.suffix == '.gz':
                data = gzip.decompress(data)
            snapshot.write_bytes(data)
            result = subprocess.run([args.ibd2sdi, str(snapshot)], check=True, capture_output=True)
            records = json.loads(result.stdout)
            assert records[0] == 'ibd2sdi'
            name = source.parent.name + '_' + source.name.split('.')[0]
            expected = name + '.expected.json.gz'
            (out / expected).write_bytes(gzip.compress(result.stdout, mtime=0))
            cases.append({'source': str(source), 'expected': expected,
                          'sha256': hashlib.sha256(data).hexdigest(), 'records': len(records)-1})
    version = subprocess.run([args.ibd2sdi, '--version'], check=True, capture_output=True, text=True).stdout.strip()
    (out / 'manifest.json').write_text(json.dumps({'tool': version, 'cases': cases}, indent=2) + '\n')
    print(f'{len(cases)} files, {sum(c["records"] for c in cases)} SDI records')


if __name__ == '__main__':
    main()
