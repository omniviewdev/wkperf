import { Call } from "/wails/runtime.js";
import { createWailsProbe } from "/wailsprobe.js";

const service = "main.TextService";
const button = document.getElementById("fingerprint");
const text = document.getElementById("text");
const output = document.getElementById("result");
const autorun = new URLSearchParams(location.search).get("autorun") === "1";

function showError(error) {
    output.textContent = error instanceof Error ? error.message : String(error);
    console.error(error);
}

try {
    const probe = await createWailsProbe({ callByName: Call.ByName });
    async function fingerprint() {
        button.disabled = true;
        try {
            await probe.calibrate();
            let digest;
            const before = probe.markInspectorClock();
            let after;
            try {
                digest = await probe.callByName(`${service}.Fingerprint`, text.value);
            } finally {
                after = probe.markInspectorClock();
            }
            output.textContent = digest;
            return { digest, inspectorBounds: Number(before) + Number(after) };
        } finally {
            try {
                await probe.flush();
            } finally {
                button.disabled = false;
            }
        }
    }

    button.addEventListener("click", () => fingerprint().catch(showError));
    if (autorun) {
        if (!probe.enabled) throw new Error("auto-run requires an enabled capture");
        await Call.ByName(`${service}.WaitForTrace`);
        const { digest, inspectorBounds } = await fingerprint();
        await Call.ByName(`${service}.ReportSmokeDigest`, digest, inspectorBounds);
    } else {
        button.disabled = false;
    }
} catch (error) {
    showError(error);
}
