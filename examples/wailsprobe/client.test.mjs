import assert from "node:assert/strict";
import { test } from "node:test";
import { createWailsProbe } from "../../pkg/wailsprobe/client.js";

test("disabled calls preserve the original cancellable binding promise", async () => {
    const promise = Promise.resolve("app result");
    let cancelled = false;
    promise.cancel = () => { cancelled = true; };
    const binding = "main.TextService.Fingerprint";
    const calls = [];
    const probe = await createWailsProbe({
        callByName(name, ...args) {
            calls.push([name, ...args]);
            if (name === "github.com/omniviewdev/wkperf/pkg/wailsprobe.Service.Clock") return null;
            assert.equal(name, binding);
            return promise;
        },
    });

    assert.equal(probe.enabled, false);
    assert.equal(probe.markInspectorClock(), false);
    assert.equal(probe.call(tx => {
        assert.equal(tx, "");
        return promise;
    }), promise);
    const byName = probe.callByName(binding, "abc");
    assert.equal(byName, promise);
    byName.cancel();
    assert.equal(cancelled, true);
    assert.equal(await byName, "app result");
    assert.equal(await probe.calibrate(), null);
    await probe.flush();
    assert.deepEqual(calls, [
        ["github.com/omniviewdev/wkperf/pkg/wailsprobe.Service.Clock", ""],
        [binding, "", "abc"],
    ]);
});

const hostClock = () => ({ clock: "go.process_monotonic", unit: "ns", ts: 1, pid: 2 });

test("Inspector marks bracket exact unique console timestamps", async t => {
    const markers = [];
    t.mock.method(console, "timeStamp", label => {
        markers.push({ label, ts: performance.now() });
    });
    const rows = [];
    const probe = await createWailsProbe({
        clock: hostClock,
        recordEvents: events => rows.push(...events),
    });

    assert.equal(probe.markInspectorClock(), true);
    assert.equal(probe.markInspectorClock(), true);
    assert.equal(rows.length, 0);
    await probe.flush();

    assert.equal(markers.length, 2);
    assert.equal(rows.length, 2);
    assert.notEqual(rows[0].tx, rows[1].tx);
    assert.ok(rows[0].document);
    for (let i = 0; i < rows.length; i++) {
        const row = rows[i];
        assert.equal(row.stage, "webkit.clock.anchor");
        assert.equal(row.phase, "mark");
        assert.equal(row.document, rows[0].document);
        assert.equal(row.timeOrigin, performance.timeOrigin);
        assert.equal(markers[i].label, `wkperf.clock.${row.tx}`);
        assert.ok(row.detail.jsBefore <= markers[i].ts && markers[i].ts <= row.detail.jsAfter);
        assert.equal(row.ts, row.detail.jsAfter);
    }
});

test("missing console timestamp produces no Inspector row", async t => {
    const original = Object.getOwnPropertyDescriptor(console, "timeStamp");
    Object.defineProperty(console, "timeStamp", { configurable: true, value: undefined });
    t.after(() => {
        if (original) Object.defineProperty(console, "timeStamp", original);
        else delete console.timeStamp;
    });
    const probe = await createWailsProbe({
        clock: hostClock,
        recordEvents: () => assert.fail("no rows should be sent"),
    });
    assert.equal(probe.markInspectorClock(), false);
    await probe.flush();
});
