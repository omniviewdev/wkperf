(function () {
  "use strict";
  const host = document.currentScript.previousElementSibling;
  const data = JSON.parse(host.querySelector(".wk-flame-data").textContent);
  const search = host.querySelector("[data-action=search]");
  const focusInput = host.querySelector("[data-action=focus]");
  const invert = host.querySelector("[data-action=invert]");
  const minWidth = host.querySelector("[data-action=minwidth]");
  const status = host.querySelector(".wk-flame-status");
  const tooltip = host.querySelector(".wk-flame-tooltip");
  const charts = Array.from(host.querySelectorAll(".wk-flame-chart"));
  const states = charts.map(function (_, i) { return { chart: data.charts[i], path: [] }; });
  let expression = null;
  let invalidSearch = false;
  const number = function (n) { return Number(n || 0).toFixed(3).replace(/\.?0+$/, ""); };
  const pct = function (n, total) { return total ? number(100 * n / total) + "%" : "0%"; };
  const frameName = function (node) { return (node.original && node.original.name) || node.frame.name || "(anonymous)"; };
  const location = function (frame) {
    return (frame.url || "(no file)") + ":" + (frame.line || 0) + ":" + (frame.column || 0);
  };
  const rootOf = function (state) { return invert.checked ? state.chart.inverted.root : state.chart.tree.root; };
  const atPath = function (root, path) {
    let node = root;
    for (const i of path) {
      if (!node.children || !node.children[i]) return root;
      node = node.children[i];
    }
    return node;
  };
  const weight = function (node) { return Number(node.layoutMs === undefined ? node.totalMs : node.layoutMs); };
  function describe(node, treeRoot) {
    const lines = [frameName(node), "Generated: " + (node.frame.name || "(anonymous)") + " · " + location(node.frame)];
    if (node.original) {
      lines.push("Original: " + (node.original.name || frameName(node)) + " · " + location(node.original));
    }
    lines.push("Category: " + node.category);
    lines.push("Self: " + number(node.selfMs) + " ms (" + pct(node.selfMs, treeRoot.totalMs) + ")");
    lines.push("Total: " + number(node.totalMs) + " ms (" + pct(node.totalMs, treeRoot.totalMs) + ")");
    if (data.mode === "diff") {
      lines.push("Baseline: " + number(node.baselineMs) + " ms");
      lines.push("Current: " + number(node.currentMs) + " ms");
      lines.push("Delta: " + (node.deltaMs >= 0 ? "+" : "") + number(node.deltaMs) + " ms");
    }
    return lines.join("\n");
  }
  function color(node, root) {
    if (data.mode !== "diff") return "";
    const delta = Number(node.deltaMs || 0);
    const alpha = 0.25 + 0.65 * Math.min(1, Math.abs(delta) / (weight(root) || 1));
    if (delta > 0) return "rgba(220, 60, 65, " + alpha + ")";
    if (delta < 0) return "rgba(35, 150, 105, " + alpha + ")";
    return "#718096";
  }
  function hideTooltip() { tooltip.hidden = true; }
  function showTooltip(event, node, root) {
    tooltip.textContent = describe(node, root);
    tooltip.hidden = false;
    const bounds = host.getBoundingClientRect();
    const x = event.clientX === undefined ? bounds.left + 12 : event.clientX;
    const y = event.clientY === undefined ? bounds.top + 12 : event.clientY;
    tooltip.style.left = Math.max(0, Math.min(x - bounds.left + 12, bounds.width - tooltip.offsetWidth - 8)) + "px";
    tooltip.style.top = Math.max(0, y - bounds.top + 18) + "px";
  }
  function renderOne(index) {
    const holder = charts[index];
    const state = states[index];
    const root = rootOf(state);
    const selected = atPath(root, state.path);
    const canvas = holder.querySelector(".wk-flame-canvas");
    const crumbs = holder.querySelector(".wk-flame-breadcrumb");
    const treeWidth = Math.max(1, canvas.clientWidth);
    const threshold = Math.max(0, Number(minWidth.value) || 0);
    const nodes = [];
    let maxDepth = 0;
    let matching = 0;
    const collect = function (node, left, span, depth, path) {
      if (span * treeWidth / 100 < threshold && depth > 0) return;
      if (weight(node) < Number(data.minMs || 0) && depth > 0) return;
      const match = expression && expression.test(frameName(node) + " " + node.frame.name + " " + location(node.frame) + " " + (node.original ? location(node.original) : ""));
      if (match) matching++;
      nodes.push({ node: node, left: left, width: span, depth: depth, path: path, match: match });
      maxDepth = Math.max(maxDepth, depth);
      let childLeft = left;
      (node.children || []).forEach(function (child, i) {
        const childSpan = weight(node) > 0 ? span * weight(child) / weight(node) : 0;
        collect(child, childLeft, childSpan, depth + 1, path.concat(i));
        childLeft += childSpan;
      });
    };
    collect(selected, 0, 100, 0, state.path);
    canvas.replaceChildren();
    canvas.style.height = ((maxDepth + 1) * 25 + 2) + "px";
    for (const entry of nodes) {
      const node = entry.node;
      const button = document.createElement("button");
      button.type = "button";
      button.className = "wk-flame-node wk-flame-" + node.category + (entry.match ? " wk-flame-match" : "");
      button.style.left = entry.left + "%";
      button.style.width = entry.width + "%";
      button.style.bottom = (entry.depth * 25) + "px";
      const shade = color(node, selected);
      if (shade) button.style.backgroundColor = shade;
      button.textContent = frameName(node);
      button.title = describe(node, root);
      button.setAttribute("aria-label", describe(node, root) + ". Click to zoom.");
      button.addEventListener("click", function () { state.path = entry.path; hideTooltip(); renderOne(index); });
      button.addEventListener("mousemove", function (event) { showTooltip(event, node, root); });
      button.addEventListener("mouseleave", hideTooltip);
      button.addEventListener("focus", function (event) { showTooltip(event, node, root); });
      button.addEventListener("blur", hideTooltip);
      canvas.appendChild(button);
    }
    crumbs.replaceChildren();
    let ancestor = root;
    const addCrumb = function (node, path) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = frameName(node);
      button.addEventListener("click", function () { state.path = path; renderOne(index); });
      crumbs.appendChild(button);
    };
    addCrumb(root, []);
    state.path.forEach(function (child, depth) {
      ancestor = ancestor.children[child];
      addCrumb(ancestor, state.path.slice(0, depth + 1));
    });
    holder.querySelector(".wk-flame-summary").textContent = number(selected.totalMs) + " ms · " + pct(selected.totalMs, root.totalMs) + " of sampled time" + (expression ? " · " + matching + " matching frames" : "") + (invert.checked ? " · self → callers" : " · root → leaf");
  }
  function render() {
    hideTooltip();
    states.forEach(function (_, index) { renderOne(index); });
    status.textContent = invalidSearch ? "Invalid regular expression. Fix search to highlight frames." : "Click any frame to zoom. Use breadcrumbs or Reset to return.";
  }
  search.addEventListener("input", function () {
    invalidSearch = false;
    try { expression = search.value ? new RegExp(search.value, "i") : null; }
    catch (_) { expression = null; invalidSearch = true; }
    search.setAttribute("aria-invalid", String(invalidSearch));
    render();
  });
  invert.addEventListener("change", function () { states.forEach(function (state) { state.path = []; }); render(); });
  minWidth.addEventListener("input", render);
  host.querySelector("[data-action=reset]").addEventListener("click", function () {
    states.forEach(function (state) { state.path = []; });
    focusInput.value = "";
    render();
  });
  function focusSelector() {
    const selector = focusInput.value.toLowerCase();
    let found = false;
    states.forEach(function (state) {
      let best = null;
      const visit = function (node, path) {
        if (!best && (frameName(node) + " " + location(node.frame) + " " + (node.original ? location(node.original) : "")).toLowerCase().includes(selector)) best = path;
        (node.children || []).forEach(function (child, i) { visit(child, path.concat(i)); });
      };
      if (selector) visit(rootOf(state), []);
      if (best) { state.path = best; found = true; }
      else if (!selector) { state.path = []; found = true; }
    });
    render();
    if (!found) status.textContent = "No frame matches focus selector.";
  }
  host.querySelector("[data-action=apply-focus]").addEventListener("click", focusSelector);
  focusInput.addEventListener("keydown", function (event) { if (event.key === "Enter") focusSelector(); });
  if (typeof ResizeObserver !== "undefined") {
    new ResizeObserver(render).observe(host.querySelector(".wk-flame-charts"));
  } else {
    window.addEventListener("resize", render);
  }
  render();
}());
