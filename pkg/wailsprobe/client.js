const service = "github.com/omniviewdev/wkperf/pkg/wailsprobe.Service";
const goClock = "go.process_monotonic";
const maxBatch = 128;
const maxPending = 8192;

let documentID;
let sequence = 0;

function documentIdentity() {
    return documentID ??= Array.from(crypto.getRandomValues(new Uint8Array(16)),
        byte => byte.toString(16).padStart(2, "0")).join("");
}

function checkClock(reading, pid) {
    if (reading?.clock !== goClock || reading.unit !== "ns" ||
        !Number.isSafeInteger(reading.ts) || reading.ts < 0 ||
        !Number.isSafeInteger(reading.pid) || reading.pid <= 0 ||
        (pid !== undefined && reading.pid !== pid)) {
        throw new Error("wailsprobe: invalid host clock reading");
    }
    return reading;
}

/**
 * Create a probe for the current document. Pass the stock Wails Call.ByName,
 * or bound clock(tx) and recordEvents(events) callbacks instead. Initializing
 * involves one service call, even when recording is disabled.
 */
export async function createWailsProbe({ callByName, clock, recordEvents }) {
    if (typeof callByName === "function") {
        clock ??= tx => callByName(`${service}.Clock`, tx);
        recordEvents ??= events => callByName(`${service}.RecordEvents`, events);
    }
    if (typeof clock !== "function" || typeof recordEvents !== "function") {
        throw new TypeError("wailsprobe: pass Call.ByName or both service callbacks");
    }

    const initial = await clock("");
    const enabled = initial !== null;
    const pid = enabled ? checkClock(initial).pid : undefined;
    const document = enabled ? documentIdentity() : undefined;
    const timeOrigin = enabled ? performance.timeOrigin : undefined;
    let pending = [];
    let dropped = 0;
    let previousFlush = Promise.resolve();

    function event(stage, phase, tx, ts, detail) {
        return { stage, phase, tx, ts, document, timeOrigin, detail };
    }

    const nextTransaction = () => `${document}-${++sequence}`;

    function enqueue(events) {
        if (pending.length + events.length > maxPending) {
            dropped += events.length;
            return;
        }
        pending.push(...events);
    }

    async function measure(invoke, calibration = false) {
        const tx = nextTransaction();
        const send = performance.now();
        let response;
        let success = false;
        try {
            response = await invoke(tx);
            success = true;
        } finally {
            const receive = performance.now();
            const events = [
                event("bridge.js.call", "begin", tx, send, calibration ? { calibration: 1 } : undefined),
                event("bridge.js.call", "end", tx, receive, success ? undefined : { failed: 1 }),
            ];
            if (calibration && success) {
                try {
                    const reading = checkClock(response, pid);
                    events.push(event("clock.anchor", "mark", tx, receive, {
                        jsSend: send, jsReceive: receive, goTs: reading.ts,
                        goClock: reading.clock, goUnit: reading.unit, timeOrigin,
                    }));
                } finally {
                    enqueue(events);
                }
            } else {
                enqueue(events);
            }
        }
        return response;
    }

    // Keep the binding's original promise (including cancel) when disabled.
    const call = invoke => enabled ? measure(invoke) : invoke("");

    return {
        enabled,
        // Time one already-bound app method. It must forward tx unchanged.
        call,
        // Convenience for a Wails binding that accepts tx as its first argument.
        callByName(name, ...args) {
            if (typeof callByName !== "function") {
                throw new TypeError("wailsprobe: callByName requires Call.ByName");
            }
            return call(tx => callByName(name, tx, ...args));
        },
        calibrate: () => enabled ? measure(clock, true) : Promise.resolve(null),
        // An attached Inspector must capture the corresponding marker itself.
        markInspectorClock() {
            const inspectorConsole = globalThis.console;
            if (!enabled || typeof inspectorConsole?.timeStamp !== "function") return false;

            const tx = nextTransaction();
            const marker = `wkperf.clock.${tx}`;
            const jsBefore = performance.now();
            inspectorConsole.timeStamp(marker);
            const jsAfter = performance.now();
            enqueue([event("webkit.clock.anchor", "mark", tx, jsAfter, { jsBefore, jsAfter })]);
            return true;
        },
        // Send a snapshot of completed measurements, never the calls in flight.
        flush() {
            const events = pending;
            const overflow = dropped;
            pending = [];
            dropped = 0;
            const flushed = previousFlush.then(async () => {
                for (let i = 0; i < events.length; i += maxBatch) {
                    await recordEvents(events.slice(i, i + maxBatch));
                }
                if (overflow) throw new Error(`wailsprobe: ${overflow} events dropped before flush`);
            });
            // Allow later snapshots after a failed write. Do not retry an
            // uncertain batch: duplicates would invalidate exact-ID joins.
            previousFlush = flushed.catch(() => {});
            return flushed;
        },
    };
}
