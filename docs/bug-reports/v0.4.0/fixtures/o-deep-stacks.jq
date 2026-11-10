# usage: jq -n --argjson depth D --argjson deep K --argjson shallow S -f gen.jq
# Flat WebKit export: S shallow samples (depth 5) plus K distinct deep stacks (depth D).
# stackFrames[0] is the leaf; timestamps lie inside [startTime,endTime].
def frame($i; $name): {sourceID: "2", name: $name, line: ($i + 1), column: 1, url: "app.js"};
def chain($n): [range(0; $n) | frame(.; "f\(.)")];
{
  startTime: 0, endTime: 20,
  records: [],
  markers: [],
  samples: [{
    stackTraces: (
      [range(0; $shallow) as $s | {timestamp: (1 + $s * 0.01), stackFrames: ([frame(900; "leaf")] + (chain(4) | reverse))}]
      + [range(0; $deep) as $k | {timestamp: (10 + $k * 0.01), stackFrames: ([frame(1000 + $k; "deepLeaf\($k)")] + (chain($depth - 1) | reverse))}]
    ),
    durations: ([range(0; $shallow) | 0.001] + [range(0; $deep) | 0.001])
  }]
}
