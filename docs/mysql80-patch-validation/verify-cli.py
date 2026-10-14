"""Reproduce CLI/official checks against the independent SQL fixture captures."""
import argparse
import base64
import gzip
import json
import pathlib
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('--reader', required=True)
parser.add_argument('--official', action='store_true')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[2]
out = pathlib.Path(__file__).resolve().parent
report = dict(status='incomplete', versions={})
report_path = out / ('cli-report.json' if args.official else 'cli-sql-report.json')


def cell(value):
    kind = value['type']
    data = value.get('value')
    if kind == 'null':
        return None
    if kind.startswith(('int', 'uint')):
        return int(data)
    if kind.startswith('float'):
        return float(data)
    if kind == 'binary':
        return base64.b64decode(data).hex().upper()
    if kind == 'geometry':
        return dict(srid=data['srid'], wkb_hex=base64.b64decode(data['wkb']).hex().upper())
    if kind == 'json':
        return tree(data)
    return data


def tree(value):
    kind = value['kind']
    if kind == 'null':
        return None
    if kind == 'array':
        return [tree(v) for v in value.get('elements', [])]
    if kind == 'object':
        return {v['key']: tree(v['value']) for v in value.get('members', [])}
    return cell(value['scalar'])


def run(command):
    p = subprocess.run(command, capture_output=True, text=True, timeout=60)
    if p.returncode:
        raise RuntimeError(str(command) + '\n' + p.stderr[:1200])
    return p


for directory in sorted((root / 'testdata/mysql80-patches').iterdir()):
    manifest = json.loads((directory / 'manifest.json').read_text())
    version = directory.name
    current = dict(cases=[], status='incomplete')
    report['versions'][version] = current
    server = json.loads((out / ('server-' + version + '.json')).read_text())
    for case in manifest['cases']:
        name = case['name']
        with tempfile.TemporaryDirectory(prefix='innodb-80-cli-') as temporary:
            path = pathlib.Path(temporary) / 'table.ibd'
            path.write_bytes(gzip.decompress((directory / (name + '.ibd.gz')).read_bytes()))
            meta = json.loads(run([args.reader, 'metadata', str(path)]).stdout)
            assert meta['MaterializedSchema'] is not None
            flags = ['--materialized'] if case['virtual'] else []
            p = run([args.reader, 'export'] + flags + [str(path)])
            diagnostics = [json.loads(line) for line in p.stderr.splitlines() if line]
            assert diagnostics[-1]['complete']
            wire = [json.loads(line) for line in p.stdout.splitlines()]
            assert wire[0]['kind'] == 'schema' and wire[-1]['kind'] == 'end'
            assert int(wire[-1]['rows']) == case['rows']
            assert len([r for r in wire if r['kind'] == 'row']) == case['rows']
            got = [[cell(v) for v in r['values']] for r in wire if r['kind'] == 'row']
            expected = json.loads(gzip.decompress((directory / (name + '.expected.json.gz')).read_bytes()))
            canonical = lambda row: json.dumps(row, sort_keys=True, ensure_ascii=False, separators=(',', ':'))
            actual, wanted = list(map(canonical, got)), list(map(canonical, expected))
            if case['unordered']:
                actual.sort()
                wanted.sort()
            assert actual == wanted, (version, name, 'CLI values differ from SQL')
            p = run([args.reader, 'check', '--scope', 'rows'] + flags + [str(path)])
            assert json.loads(p.stdout)['Complete']
            for index in ('s', 'px') if name.startswith('secondary_') else ():
                run([args.reader, 'check', '--scope', 'secondary', '--index', index, str(path)])
                query = pathlib.Path(temporary) / 'query.json'
                query.write_text('{}\n')
                run([args.reader, 'query', '--index', index, '--query', str(query), str(path)])
            item = dict(name=name, rows=case['rows'], metadata='passed', export='passed', check_rows='passed')
            if args.official:
                target = '/tmp/innodb-reader-check.ibd'
                run(['docker', 'cp', str(path), server['container'] + ':' + target])
                run(['docker', 'exec', server['container'], 'innochecksum', target])
                item['innochecksum'] = 'passed'
                run(['docker', 'exec', server['container'], 'rm', target])
            current['cases'].append(item)
    current['status'] = 'complete'
    print(version, len(current['cases']), 'CLI/official cases passed', flush=True)
    report_path.write_text(json.dumps(report, indent=2) + '\n')
report['status'] = 'complete'
report_path.write_text(json.dumps(report, indent=2) + '\n')
