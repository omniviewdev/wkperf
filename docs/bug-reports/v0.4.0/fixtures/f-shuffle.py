"""shuffle.py IN OUT SEED: shuffle (stackTrace, duration) pairs and records; keep everything else."""
import json, random, sys
src, dst, seed = sys.argv[1], sys.argv[2], int(sys.argv[3])
d = json.load(open(src))
r = d.get('recording', d)
rng = random.Random(seed)
for t in r['samples']:
    pairs = list(zip(t['stackTraces'], t['durations']))
    assert len(pairs) == len(t['stackTraces']) == len(t['durations'])
    rng.shuffle(pairs)
    t['stackTraces'] = [p[0] for p in pairs]
    t['durations'] = [p[1] for p in pairs]
rng.shuffle(r['records'])
json.dump(d, open(dst, 'w'))
