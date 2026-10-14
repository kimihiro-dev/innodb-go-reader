"""Reproduce driver, manifest, export and official checks from fixed SQL captures."""
import argparse
import base64
import gzip
import json
import pathlib
import subprocess
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('--reader', required=True)
parser.add_argument('--linux-reader', required=True)
parser.add_argument('--resume', action='store_true')
args = parser.parse_args()
out = pathlib.Path(__file__).resolve().parent
root = out.parents[1]
report = dict(status='incomplete', versions={})
if args.resume and (out / 'cli-report.json').exists():
    report = json.loads((out / 'cli-report.json').read_text())


def run(command, success=True):
    p = subprocess.run(command, capture_output=True, text=True, timeout=120)
    if success and p.returncode:
        raise RuntimeError(str(command) + '\n' + p.stderr[:1600])
    return p


def normalize(value):
    if isinstance(value, dict):
        result = {k: normalize(v) for k, v in value.items()}
        if result.get('max_chars') == 0:
            result.pop('max_chars')
        if result.get('nullable') is False:
            result.pop('nullable')
        if result.get('indexes') == []:
            result.pop('indexes')
        return result
    if isinstance(value, list):
        return [normalize(v) for v in value]
    return value


def cell(value):
    kind, data = value['type'], value.get('value')
    if kind == 'null':
        return None
    if kind.startswith(('int', 'uint')):
        return int(data)
    if kind.startswith('float'):
        return float(data)
    if kind == 'binary':
        return base64.b64decode(data).hex().upper()
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


canonical = lambda v: json.dumps(v, sort_keys=True, ensure_ascii=False, separators=(',', ':'))
for directory in sorted((root / 'testdata/partition-patches').iterdir()):
    version = directory.name
    if args.resume and report['versions'].get(version, {}).get('status') == 'complete':
        print(version, 'previous completed checks retained', flush=True)
        continue
    server = json.loads((out / ('server-' + version + '.json')).read_text())
    container = server['container']
    manifest = json.loads((directory / 'manifest.json').read_text())
    current = dict(collections=[], driver=[], status='incomplete')
    report['versions'][version] = current
    if version.startswith('5.'):
        run(['docker', 'cp', args.linux_reader, container + ':/tmp/innodb-partition-reader'])
        last = {c['table']: c for c in manifest['cases']}
        for c in last.values():
            destination = '/tmp/innodb-partition-driver-' + c['name']
            # Regenerate into a fresh directory on every verifier run.
            destination += '-' + next(tempfile._get_candidate_names())
            p = run(['docker', 'exec', '--env', 'PATH=', container, '/tmp/innodb-partition-reader', 'schema', '--socket', '/var/run/mysqld/mysqld.sock',
                     '--database', manifest['database'], '--table', c['table'], '--partitions', '--indexes', '--output', destination], success=c['supported'])
            if c['supported']:
                actual = json.loads(run(['docker', 'exec', container, 'cat', destination + '/partitions.json']).stdout)
                wanted = json.loads((directory / (c['name'] + '.partitions.json')).read_text())
                assert normalize(actual) == normalize(wanted), (version, c['name'], 'native driver definition mismatch')
                run(['docker', 'exec', container, 'rm', '-r', destination])
            else:
                assert p.returncode != 0
            current['driver'].append(dict(table=c['table'], expected='passed' if c['supported'] else 'rejected'))
    for c in manifest['cases']:
        with tempfile.TemporaryDirectory(prefix='innodb-partition-cli-') as temp:
            temp = pathlib.Path(temp)
            files = []
            for i, f in enumerate(c['files']):
                path = temp / (str(i) + '.ibd')
                path.write_bytes(gzip.decompress((directory / f['file']).read_bytes()))
                files.append(str(path))
                run(['docker', 'cp', str(path), container + ':/tmp/innodb-partition-check.ibd'])
                run(['docker', 'exec', container, 'innochecksum', '/tmp/innodb-partition-check.ibd'])
                run(['docker', 'exec', container, 'rm', '/tmp/innodb-partition-check.ibd'])
            destination = temp / 'files.json'
            flags = []
            if version.startswith('5.'):
                if not c['supported']:
                    current['collections'].append(dict(name=c['name'], supported=False, innochecksum='passed'))
                    continue
                definition = temp / 'partitions.json'
                definition.write_bytes((directory / (c['name'] + '.partitions.json')).read_bytes())
                flags = ['--partition-schema', str(definition)]
            p = run([args.reader, 'manifest'] + flags + ['--output', str(destination)] + list(reversed(files)), success=c['supported'])
            if not c['supported']:
                assert p.returncode != 0 and not destination.exists()
                current['collections'].append(dict(name=c['name'], supported=False, manifest='rejected', innochecksum='passed'))
                continue
            value = json.loads(destination.read_text())
            assert value['version'] == (2 if flags else 1)
            run([args.reader, 'metadata', '--manifest', str(destination)])
            materialized = ['--materialized'] if c['virtual'] else []
            p = run([args.reader, 'export', '--manifest', str(destination)] + materialized)
            wire = [json.loads(s) for s in p.stdout.splitlines()]
            assert wire[0]['kind'] == 'schema' and wire[-1]['kind'] == 'end' and int(wire[-1]['rows']) == c['rows']
            actual = {f['partition']: [] for f in c['files']}
            order = []
            for row in wire[1:-1]:
                source = row['physical']['partition']
                order.append(source['Number'])
                actual[source['Name']].append(canonical([cell(v) for v in row['values']]))
            assert order == sorted(order)
            expected = json.loads(gzip.decompress((directory / (c['name'] + '.expected.json.gz')).read_bytes()))
            for name, rows in expected['partitions'].items():
                wanted = list(map(canonical, rows))
                if c['unordered']:
                    actual[name].sort()
                    wanted.sort()
                assert actual[name] == wanted, (version, c['name'], name, 'CLI/SQL mismatch')
            p = run([args.reader, 'check', '--manifest', str(destination)] + materialized)
            assert json.loads(p.stdout)['Complete']
            current['collections'].append(dict(name=c['name'], supported=True, rows=c['rows'], manifest='passed', CLI_SQL='passed', innochecksum='passed'))
    current['status'] = 'complete'
    (out / 'cli-report.json').write_text(json.dumps(report, indent=2) + '\n')
    print(version, len(current['collections']), 'collections verified', flush=True)
report['status'] = 'complete'
(out / 'cli-report.json').write_text(json.dumps(report, indent=2) + '\n')
