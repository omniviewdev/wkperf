"""Generate a tiny handcrafted WebKit recording with independently known answers.

usage: gen_tiny.py OUT [--scale-b K] [--drop-c] [--add-d] [--shuffle SEED] [--wrapped]
Stacks are written leaf-first (stackFrames[0] = leaf), as WebKit's consumer reads them.
"""
import json, random, sys

def frame(name, url, line, col, sid):
    return {"sourceID": sid, "name": name, "line": line, "column": col, "url": url}

MAIN = frame("main", "file:///app/app.js", 1, 1, "1")
A = frame("A", "file:///app/app.js", 10, 5, "1")
B = frame("B", "file:///app/app.js", 20, 5, "1")
C = frame("C", "file:///app/lib.js", 5, 3, "2")
D = frame("D", "file:///app/app.js", 30, 5, "1")
GA = frame("getAnimations", "", -1, -1, "4294967295")

def build(args):
    scale_b = 1.0
    if "--scale-b" in args:
        scale_b = float(args[args.index("--scale-b") + 1])
    # (relative ts, leaf-first stack, seconds)
    samples = [
        (1.0, [B, A, MAIN], 0.010),
        (2.0, [C, A, MAIN], 0.020),
        (3.0, [A, MAIN], 0.005),
        (4.0, [GA, B, MAIN], 0.030),
        (5.0, [B, A, MAIN], 0.010),
        (6.5, [A, A, MAIN], 0.004),
        (8.0, [C, B, A, MAIN], 0.001),
    ]
    if "--drop-c" in args:
        samples = [s for s in samples if s[1][0] is not C]
    if "--add-d" in args:
        samples.append((9.0, [D, MAIN], 0.007))
    out = []
    for ts, stack, dur in samples:
        if stack[0] is B:
            dur *= scale_b
        out.append((ts, stack, dur))
    if "--shuffle" in args:
        random.Random(int(args[args.index("--shuffle") + 1])).shuffle(out)
    start = 100.0
    records = [
        {"type": "timeline-record-type-script", "eventType": "timer-fired", "startTime": start + 2.0, "endTime": start + 2.08, "details": "1"},
        {"type": "timeline-record-type-script", "eventType": "event-dispatched", "startTime": start + 4.0, "endTime": start + 4.06, "details": "click"},
        {"type": "timeline-record-type-script", "eventType": "event-dispatched", "startTime": start + 5.0, "endTime": start + 5.012, "details": "click"},
    ]
    if "--shuffle" in args:
        random.Random(int(args[args.index("--shuffle") + 1]) + 1).shuffle(records)
    rec = {
        "displayName": "tiny",
        "startTime": start,
        "endTime": start + 10.0,
        "instrumentTypes": ["timeline-record-type-script"],
        "records": records,
        "markers": [],
        "memoryPressureEvents": [],
        "samples": [{
            "target": {"identifier": "page-1", "type": "page", "name": "Page", "url": ""},
            "stackTraces": [{"timestamp": start + ts, "stackFrames": stack} for ts, stack, _ in out],
            "durations": [d for _, _, d in out],
        }],
    }
    if "--wrapped" in args:
        return {"version": 1, "recording": rec, "overview": {}}
    return rec

if __name__ == "__main__":
    json.dump(build(sys.argv[2:]), open(sys.argv[1], "w"), indent=1)
