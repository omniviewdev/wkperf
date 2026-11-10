def fr($n; $l): {sourceID: "7", name: $n, line: $l, column: 1, url: "https://a.test/app.js"};
def st($ts; $leaf; $l): {timestamp: $ts, stackFrames: [fr($leaf; $l), fr("appRoot"; 1)]};
{version: 1, overview: {}, recording: {
  displayName: "untimed-samples", startTime: 10, endTime: 12, discontinuities: [],
  instrumentTypes: ["timeline-record-type-script"],
  records: [
    {type: "timeline-record-type-script", eventType: "timer-fired", startTime: 10.2, endTime: 10.3, details: "1"},
    {type: "timeline-record-type-script", eventType: "timer-fired", startTime: 11.0, endTime: 11.04, details: "2"}],
  markers: [], memoryPressureEvents: [],
  samples: [{target: {identifier: "page-1", type: "page", name: "Page"},
    stackTraces: ([range(25) | st(0; "untimedLeaf"; 10)] + [range(15) | st(0; "sharedLeaf"; 20)]
                  + [range(10) as $i | st(11 + $i * 0.004; "sharedLeaf"; 20)]),
    durations: ([range(40) | 0.001] + [range(10) | 0.004])}]}}
