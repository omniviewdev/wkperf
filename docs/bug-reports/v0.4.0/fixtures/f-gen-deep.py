#!/usr/bin/env python3
"""Write a synthetic WebKit Timelines export with deep, distinct stacks.

usage: python3 f-gen-deep.py OUT.json [SAMPLES=120] [DEPTH=400]

Every sample has a timestamp inside [startTime, endTime), a 1 ms duration, and a
root-to-leaf chain of DEPTH frames (React-like recursion on one bundle URL) that
ends in a distinct leaf, so every sample is a distinct complete stack.
Stacks are written leaf-first, as WebKit exports them.
"""
import json, sys

out = sys.argv[1]
samples = int(sys.argv[2]) if len(sys.argv) > 2 else 120
depth = int(sys.argv[3]) if len(sys.argv) > 3 else 400
url = "wails://wails.localhost/assets/index-deadbeef.js"
names = ["recursivelyTraversePassiveMountEffects", "commitPassiveMountOnFiber"]
traces = []
for i in range(samples):
    chain = [{"sourceID": "2", "name": "performWorkUntilDeadline", "line": 100, "column": 33, "url": url}]
    for d in range(depth - 2):
        n = names[d % 2]
        chain.append({"sourceID": "2", "name": n, "line": 2000 + d % 2, "column": 12 + len(n), "url": url})
    chain.append({"sourceID": "2", "name": "leaf%03d" % i, "line": 5000 + i, "column": 20, "url": url})
    traces.append({"timestamp": 10.0 + i * 0.01, "stackFrames": list(reversed(chain))})
rec = {"displayName": "deep", "startTime": 10.0, "endTime": 12.0, "instrumentTypes": [], "records": [], "markers": [],
       "memoryPressureEvents": [], "samples": [{"target": {"identifier": "page-1", "type": "page", "name": "Page", "url": ""},
                                                "stackTraces": traces, "durations": [0.001] * samples}]}
json.dump({"version": 1, "recording": rec, "overview": {}}, open(out, "w"))
