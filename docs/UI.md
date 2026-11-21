# Local recording investigator

Run `wkperf ui --open`. If the browser does not open, paste the printed URL into your browser. Keep the terminal running while you use the UI; press Ctrl-C to stop it.

1. In Web Inspector's Timelines tab, export a recording as JSON. Include the instruments you want to investigate, such as JavaScript sampling for call stacks.
2. Choose that file under **Baseline recording**. To compare two runs, choose the second file under **Candidate recording**. Use recordings of the same action and page when possible.
3. Select **Analyze recordings**. Expand **Hot paths and functions**, **Recording metrics**, and **Findings** to inspect each run. Expand a common sampled stack to see its frames in caller-to-leaf order. All controls work with the keyboard and do not require JavaScript.
4. With two files, check the warnings and **Metric changes** before treating a difference as a regression. The UI uses the same analysis and comparison engine as the CLI. It can normalize duration-dependent values when the runs differ in length. A single recording per side does not establish run-to-run variability; use `wkperf compare` with multiple runs for that.

## Listener and file limits

```sh
wkperf ui                           # Print an available IPv4 loopback URL
wkperf ui --listen 127.0.0.1:8123   # Use a fixed loopback port
wkperf ui --listen '[::1]:0' --open # Use IPv6 loopback and open the browser
wkperf ui --max-upload-mib 96      # Allow up to 96 MiB per recording
```

`--listen` accepts numeric loopback addresses or `localhost`, which uses `127.0.0.1`. Other interfaces, wildcard addresses, and DNS hostnames are rejected. Always use the exact printed URL. Do not expose the listener through a reverse proxy or tunnel.

The default limit is 32 MiB per file. `--max-upload-mib` sets a limit between 1 and 256 MiB per file. The full multipart request is also bounded, and the server analyzes one upload at a time. Each file may have up to 250,000 entries in a single JSON array, 1,000,000 entries in total (array entries plus object fields), and 128 levels of nested objects and arrays. Large captures can require substantially more memory to decode and analyze than their size on disk. Use the CLI directly for larger recordings or workflows that need source maps, capture, custom windows, or repeat runs.

The browser uploads only files you select. The server analyzes them in memory, does not save or retain them after returning the report, and does not resolve local paths or fetch source maps. To clear the displayed report, navigate to the printed URL again or close the tab. This UI does not start captures, run commands, or send reports to other services.
