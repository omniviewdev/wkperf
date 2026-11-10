#!/usr/bin/env python3
"""Extract every `wkperf ...` reproduce command printed by `wkperf analyze` (Markdown and/or JSON),
run each one, and check that it exits 0, returns rows, and (for `query ... metrics KEY` /
`query ... rootcause KEY`) reproduces the value printed next to it (relative tolerance 2e-5,
because Markdown prints %.6g).

usage: o-repro-commands.py [--md PATH]... [--json PATH]... [--sample N] [--jobs J] [--bin wkperf]
                           [--unescape-md] [--out results.json]

By default Markdown code spans are taken literally (what an agent copies). --unescape-md undoes
wkperf's `\\|` and backslash-backtick escaping first.
"""
import argparse, concurrent.futures as cf, json, math, random, re, shlex, subprocess, sys, collections

ap = argparse.ArgumentParser()
ap.add_argument('--md', action='append', default=[])
ap.add_argument('--json', action='append', default=[])
ap.add_argument('--sample', type=int, default=0)
ap.add_argument('--jobs', type=int, default=8)
ap.add_argument('--bin', default='wkperf')
ap.add_argument('--out', default=None)
ap.add_argument('--unescape-md', action='store_true')
a = ap.parse_args()

CMD = r'`(wkperf (?:[^`\\]|\\.)*)`'
items = []  # (origin, cmd, expected value or None, context)

def num(s):
    try:
        return float(s)
    except Exception:
        return None

for path in a.md:
    for ln, line in enumerate(open(path, encoding='utf-8'), 1):
        cmds = re.findall(CMD, line)
        if a.unescape_md:
            cmds = [c.replace('\\`', '`').replace('\\|', '|') for c in cmds]
        if not cmds:
            continue
        exp = [None] * len(cmds)
        m = re.match(r'^\s*- (?:.*?): (-?[0-9.e+-]+) \S.*?\. Reproduce: `', line)
        if m and len(cmds) == 1:
            exp[0] = num(m.group(1))
        m = re.search(r'attributed impact (-?[0-9.e+-]+)ms \((-?[0-9.e+-]+)%\)\. Reproduce: ', line)
        if m and len(cmds) == 2:
            exp = [num(m.group(1)), num(m.group(2))]
        m = re.match(r'^\| (`.*?`) \| (-?[0-9.e+-]+) \| (-?[0-9.e+-]+) \| `', line)
        if m and len(cmds) == 2:
            exp = [num(m.group(2)), num(m.group(3))]
        m = re.match(r'^\| ↳ .*? \| \| (-?[0-9.e+-]+) \| `', line)
        if m and len(cmds) == 1:
            exp = [num(m.group(1))]
        m = re.match(r'^\| [^|`]+ \| (-?[0-9.e+-]+) \| `', line)  # trigger metric tables
        if m and len(cmds) == 1:
            exp = [num(m.group(1))]
        m = re.match(r'^- Top (?:self|inclusive): `.*`, (-?[0-9.e+-]+)ms\. Reproduce: `', line)
        if m and len(cmds) == 1:
            exp = [num(m.group(1))]
        m = re.match(r'^\s*- [^:]+: (-?[0-9.e+-]+)\. Reproduce: `', line)  # slow frame items
        if m and len(cmds) == 1:
            exp = [num(m.group(1))]
        for c, e in zip(cmds, exp):
            items.append(('md:%s:%d' % (path.split('/')[-1], ln), c, e, line.strip()[:160]))

def walk(v, path):
    if isinstance(v, dict):
        c = v.get('command')
        if isinstance(c, str) and c.startswith('wkperf '):
            e = v.get('value') if isinstance(v.get('value'), (int, float)) else None
            items.append(('json:' + path, c, e, str(v.get('key') or v.get('metricKey') or '')))
        for k, x in v.items():
            walk(x, path + '.' + k)
    elif isinstance(v, list):
        for x in v:
            walk(x, path + '[]')

for path in a.json:
    walk(json.load(open(path)), '$')

# dedupe identical (cmd, expected)
seen = {}
for it in items:
    seen.setdefault((it[1], it[2]), it)
uniq = list(seen.values())
if a.sample and len(uniq) > a.sample:
    random.seed(1)
    uniq = random.sample(uniq, a.sample)

def run(it):
    origin, cmd, exp, ctx = it
    try:
        argv = shlex.split(cmd)
    except ValueError as e:
        return it, 'shlex_error', str(e), None
    argv[0] = a.bin
    p = subprocess.run(argv, capture_output=True, timeout=600)
    out, err = p.stdout.decode('utf-8', 'replace'), p.stderr.decode('utf-8', 'replace').strip()
    if p.returncode != 0:
        return it, 'exit_%d' % p.returncode, err[:300], None
    if argv[1] == 'analyze':
        try:
            d = json.loads(out)
            return it, 'ok', '', None
        except Exception:
            return it, 'bad_json', out[:200], None
    try:
        d = json.loads(out)
    except Exception:
        return it, 'bad_json', out[:200], None
    if d.get('status') != 'ok':
        return it, 'status_' + str(d.get('status')), '', None
    rows = d.get('rows') or []
    if not rows:
        return it, 'no_rows', '', None
    if exp is None:
        return it, 'ok_unchecked', '', None
    op = argv[3]
    if op in ('metrics', 'rootcause') and len(argv) > 4 and not argv[4].startswith('-'):
        got = rows[0].get('value')
    else:
        return it, 'ok_unchecked', '', None
    if got is None:
        return it, 'no_value', '', None
    tol = max(abs(exp), abs(got)) * 2e-5 + 1e-9
    if abs(got - exp) <= tol:
        return it, 'ok_match', '', got
    return it, 'value_mismatch', 'printed=%r got=%r' % (exp, got), got

res = []
with cf.ThreadPoolExecutor(a.jobs) as ex:
    for r in ex.map(run, uniq):
        res.append(r)

cnt = collections.Counter(r[1] for r in res)
print('commands extracted=%d unique=%d run=%d' % (len(items), len(seen), len(res)))
for k, v in sorted(cnt.items()):
    print('  %-16s %d' % (k, v))
bad = [r for r in res if not r[1].startswith('ok')]
for r in bad[:400]:
    it = r[0]
    print('FAIL %s [%s] %s\n   cmd: %s\n   ctx: %s' % (r[1], it[0], r[2][:300], it[1][:400], it[3][:160]))
if a.out:
    json.dump([{'origin': r[0][0], 'cmd': r[0][1], 'expected': r[0][2], 'ctx': r[0][3], 'status': r[1], 'detail': r[2], 'got': r[3]} for r in res], open(a.out, 'w'), indent=1)
