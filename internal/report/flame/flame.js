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
  const states = charts.map(function (_, i) {
    const chart = data.charts[i];
    return {
      chart: chart, path: [], focused: null, functionId: null,
      indexes: [indexTree(chart.tree.root), indexTree(chart.inverted.root)],
      order: new Map(chart.functions.map(function (fn, rank) { return [fn.id, rank]; }))
    };
  });
  let expression = null;
  let invalidSearch = false;
  let focusMessage = "";
  const number = function (n) { return Number(n || 0).toFixed(3).replace(/\.?0+$/, ""); };
  const pct = function (n, total) { return total ? number(100 * n / total) + "%" : "0%"; };
  const frameName = function (node) { return (node.original && node.original.name) || node.frame.name || "(anonymous)"; };
  const location = function (frame) {
    return (frame.url || "(no file)") + ":" + (frame.line || 0) + ":" + (frame.column || 0);
  };
  const rootOf = function (state) { return invert.checked ? state.chart.inverted.root : state.chart.tree.root; };
  const indexOf = function (state) { return state.indexes[invert.checked ? 1 : 0]; };
  const weight = function (node) { return Number(node.layoutMs === undefined ? node.totalMs : node.layoutMs); };

  function indexTree(root) {
    const index = new Map();
    const visit = function (node, parent, childIndex) {
      index.set(node.id, { node: node, parent: parent, childIndex: childIndex });
      (node.children || []).forEach(function (child, i) { visit(child, node.id, i); });
    };
    visit(root, null, 0);
    return index;
  }
  function pathTo(index, id) {
    const path = [];
    let entry = index.get(id);
    while (entry && entry.parent !== null) {
      path.push(entry.childIndex);
      entry = index.get(entry.parent);
    }
    return path.reverse();
  }
  function atPath(root, path) {
    let node = root;
    for (const i of path) {
      if (!node.children || !node.children[i]) return root;
      node = node.children[i];
    }
    return node;
  }
  function describe(node, treeRoot, unit) {
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
      lines.push("Delta: " + (node.deltaMs >= 0 ? "+" : "") + number(node.deltaMs) + " " + (unit || "ms"));
    }
    return lines.join("\n");
  }
  function ariaLabel(node) {
    const name = frameName(node);
    return (name.length > 80 ? name.slice(0, 79) + "…" : name) + ", total " + number(node.totalMs) + " ms, self " + number(node.selfMs) + " ms";
  }
  function color(node, maximum) {
    const steps = (data.palette.length - 1) / 2;
    const share = maximum ? Math.max(-1, Math.min(1, Number(node.deltaMs || 0) / maximum)) : 0;
    return data.palette[Math.round(steps * (1 + share))];
  }
  function hideTooltip() { tooltip.hidden = true; }
  function showTooltip(event, node, root, unit) {
    tooltip.textContent = describe(node, root, unit);
    tooltip.hidden = false;
    const bounds = host.getBoundingClientRect();
    const button = event.currentTarget.getBoundingClientRect();
    const x = event.clientX === undefined ? button.left : event.clientX;
    const y = event.clientY === undefined ? button.bottom : event.clientY;
    tooltip.style.left = Math.max(0, Math.min(x - bounds.left + 12, bounds.width - tooltip.offsetWidth - 8)) + "px";
    tooltip.style.top = Math.max(0, y - bounds.top + 18) + "px";
  }
  // Compensated summation keeps merged context weights stable for small samples.
  function sum(nodes, field) {
    let value = 0;
    let correction = 0;
    for (const node of nodes) {
      const next = Number(node[field] || 0) - correction;
      const total = value + next;
      correction = (total - value) - next;
      value = total;
    }
    return value;
  }
  function mergeContexts(sources, state, first) {
    const groups = new Map();
    for (const source of sources) {
      for (const child of source.children || []) {
        if (!groups.has(child.frame.id)) groups.set(child.frame.id, []);
        groups.get(child.frame.id).push(child);
      }
    }
    const children = Array.from(groups.values()).sort(function (a, b) {
      return state.order.get(a[0].frame.id) - state.order.get(b[0].frame.id);
    }).map(function (group) { return mergeContexts(group, state, false); });
    const total = sum(sources, "totalMs");
    const baseline = sum(sources, "baselineMs");
    const current = sum(sources, "currentMs");
    const self = invert.checked ? (first ? total : 0) : sum(sources, "selfMs");
    let layout = total;
    if (data.mode === "diff") {
      const baselineSelf = Math.max(0, baseline - sum(children, "baselineMs"));
      const currentSelf = Math.max(0, current - sum(children, "currentMs"));
      layout = Math.max(baseline, current, sum(children, "layoutMs") + (invert.checked ? 0 : Math.max(baselineSelf, currentSelf)));
    }
    return Object.assign({}, sources[0], {
      selfMs: self, totalMs: total, layoutMs: layout, baselineMs: baseline,
      currentMs: current, deltaMs: sum(sources, "deltaMs"), children: children
    });
  }
  function clearFocus(state) {
    state.path = [];
    state.focused = null;
    state.functionId = null;
  }
  function focusFunction(state, fn) {
    const ids = invert.checked ? fn.invertedContexts : fn.contexts;
    const sources = ids.map(function (id) { return indexOf(state).get(id).node; });
    state.focused = mergeContexts(sources, state, true);
    state.functionId = fn.id;
    state.path = [];
    return ids.length;
  }
  function renderOne(index) {
    const holder = charts[index];
    const state = states[index];
    const root = rootOf(state);
    const viewRoot = state.focused || root;
    const selected = atPath(viewRoot, state.path);
    const canvas = holder.querySelector(".wk-flame-canvas");
    const crumbs = holder.querySelector(".wk-flame-breadcrumb");
    const treeWidth = Math.max(1, canvas.clientWidth);
    const threshold = Math.max(0, Number(minWidth.value) || 0);
    const nodes = [];
    let maxDepth = 0;
    let maximum = 0;
    let matching = 0;
    const collect = function (node, left, span, depth, path) {
      if (span * treeWidth / 100 < threshold && depth > 0) return;
      if (weight(node) < Number(data.minMs || 0) && depth > 0) return;
      const match = expression && expression.test(frameName(node) + " " + node.frame.name + " " + location(node.frame) + " " + (node.original ? location(node.original) : ""));
      if (match) matching++;
      nodes.push({ node: node, left: left, width: span, depth: depth, path: path, match: match });
      maxDepth = Math.max(maxDepth, depth);
      maximum = Math.max(maximum, Math.abs(Number(node.deltaMs || 0)));
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
      button.style.top = (entry.depth * 25) + "px";
      if (data.mode === "diff") {
        const paint = color(node, maximum);
        button.style.backgroundColor = paint.background;
        button.style.color = paint.foreground;
      }
      button.textContent = frameName(node);
      button.setAttribute("aria-label", ariaLabel(node));
      button.dataset.nodeId = node.id;
      button.dataset.functionId = node.frame.id || "";
      button.addEventListener("click", function () { state.path = entry.path; hideTooltip(); renderOne(index); });
      button.addEventListener("mousemove", function (event) { showTooltip(event, node, root, state.chart.unit); });
      button.addEventListener("mouseleave", hideTooltip);
      button.addEventListener("focus", function (event) { showTooltip(event, node, root, state.chart.unit); });
      button.addEventListener("blur", hideTooltip);
      canvas.appendChild(button);
    }
    crumbs.replaceChildren();
    const addCrumb = function (node, action) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = frameName(node);
      button.addEventListener("click", function () { hideTooltip(); action(); renderOne(index); });
      crumbs.appendChild(button);
    };
    addCrumb(root, function () { clearFocus(state); });
    if (state.focused) addCrumb(viewRoot, function () { state.path = []; });
    let ancestor = viewRoot;
    state.path.forEach(function (child, depth) {
      ancestor = ancestor.children[child];
      addCrumb(ancestor, function () { state.path = state.path.slice(0, depth + 1); });
    });
    holder.querySelector(".wk-flame-summary").textContent = number(selected.totalMs) + " ms · " + pct(selected.totalMs, root.totalMs) + " of sampled time" + (expression ? " · " + matching + " matching frames" : "") + (invert.checked ? " · self → callers" : " · root → leaf");
  }
  function render() {
    hideTooltip();
    states.forEach(function (_, index) { renderOne(index); });
    status.textContent = invalidSearch ? "Invalid regular expression. Fix search to highlight frames." : focusMessage || "Click any frame to zoom. Use breadcrumbs or Reset to return.";
  }
  search.addEventListener("input", function () {
    invalidSearch = false;
    try { expression = search.value ? new RegExp(search.value, "i") : null; }
    catch (_) { expression = null; invalidSearch = true; }
    search.setAttribute("aria-invalid", String(invalidSearch));
    render();
  });
  invert.addEventListener("change", function () {
    states.forEach(function (state) {
      if (state.functionId) {
        focusFunction(state, state.chart.functions.find(function (fn) { return fn.id === state.functionId; }));
      } else {
        state.path = [];
      }
    });
    render();
  });
  minWidth.addEventListener("input", render);
  host.querySelector("[data-action=reset]").addEventListener("click", function () {
    states.forEach(clearFocus);
    focusInput.value = "";
    focusInput.setAttribute("aria-invalid", "false");
    focusMessage = "";
    render();
  });
  function selectorMatcher(text) {
    if (text.startsWith("/") && text.endsWith("/") && text.length >= 2) {
      const re = new RegExp(text.slice(1, -1));
      return function (frame) {
        return re.test(frame.name) || re.test(frame.url) || (frame.original && (re.test(frame.original.name || "") || re.test(frame.original.url)));
      };
    }
    if (/^(?:f_|n_)/.test(text)) {
      if (!/^[fn]_[0-9a-fA-F]{64}$/.test(text)) throw new Error("Malformed function or node ID.");
      return function (frame) { return frame.id === text; };
    }
    if (/^(?:fn:|node:|fn-|node-)/.test(text)) throw new Error("Unknown function or node ID.");
    const last = text.lastIndexOf(":");
    const before = text.lastIndexOf(":", last - 1);
    if (last >= 0 && before >= 0) {
      const url = text.slice(0, before);
      const line = text.slice(before + 1, last);
      const column = text.slice(last + 1);
      if (!url || !/^\d+$/.test(line) || !/^\d+$/.test(column)) throw new Error("Malformed source selector: require url:line:column.");
      return function (frame) {
        const matches = function (value) { return value && value.url === url && value.line === Number(line) && value.column === Number(column); };
        return matches(frame) || matches(frame.original);
      };
    }
    if (text.startsWith("/")) throw new Error("Malformed regex selector: require /regex/.");
    return function (frame) { return frame.name === text || (!frame.name && text === "(anonymous)") || (frame.original && frame.original.name === text); };
  }
  function focusSelector() {
    const text = focusInput.value.trim();
    focusInput.setAttribute("aria-invalid", "false");
    focusMessage = "";
    try {
      if (!text) {
        states.forEach(clearFocus);
      } else if (states.some(function (state) { return indexOf(state).has(text); })) {
        states.forEach(function (state) {
          clearFocus(state);
          if (indexOf(state).has(text)) state.path = pathTo(indexOf(state), text);
        });
        focusMessage = "Focused exact calling context " + text + ".";
      } else {
        const matches = selectorMatcher(text);
        const candidates = new Map();
        states.forEach(function (state) {
          state.chart.functions.forEach(function (fn) { if (matches(fn.frame)) candidates.set(fn.id, fn); });
        });
        if (candidates.size === 0) throw new Error("No function matches focus selector. Use an exact name, location, ID, or /regex/.");
        if (candidates.size > 1) {
          const choices = Array.from(candidates.values());
          const labels = choices.slice(0, 10).map(function (fn) {
            return ((fn.frame.original && fn.frame.original.name) || fn.frame.name || "(anonymous)") + " · " + location(fn.frame) + " · " + fn.id + " · " + number(fn.totalMs) + " ms";
          });
          throw new Error("Ambiguous focus selector; choose a function ID: " + labels.join("; ") + (choices.length > 10 ? "; " + (choices.length - 10) + " more functions" : ""));
        }
        const id = candidates.keys().next().value;
        let contexts = 0;
        states.forEach(function (state) {
          clearFocus(state);
          const fn = state.chart.functions.find(function (value) { return value.id === id; });
          if (fn) contexts += focusFunction(state, fn);
        });
        focusMessage = "Focused function across " + contexts + " outermost calling context" + (contexts === 1 ? "." : "s.");
      }
    } catch (error) {
      focusInput.setAttribute("aria-invalid", "true");
      focusMessage = error.message;
    }
    render();
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
