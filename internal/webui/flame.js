"use strict";

for (const section of document.querySelectorAll("[data-flame]")) {
  const plot = section.querySelector(".flame-plot");
  if (!plot) continue;

  const layout = section.querySelector(".flame-layout");
  const readout = section.querySelector("[data-readout]");
  const mode = readout.querySelector("[data-mode]");
  const values = [...readout.querySelectorAll("[data-value]")];
  const scale = section.querySelector("[data-scale-label]");
  const zoom = readout.querySelector("[data-zoom]");
  const reset = readout.querySelector("[data-reset]");
  const rows = section.querySelectorAll("[data-context]");
  const frames = [...plot.querySelectorAll("[data-index]")].map((button, index) => ({
    button,
    row: rows[index],
    parent: Number(button.dataset.parent) - 1,
    depth: Number(button.dataset.depth),
    offset: parseFloat(button.style.getPropertyValue("--left")),
    share: parseFloat(button.style.getPropertyValue("--width")),
    name: button.textContent,
  }));
  const fullLevels = plot.style.getPropertyValue("--levels");
  let selected = frames[0];
  let tabStop = selected;
  let zoomRoot = null;

  function namePath(frame) {
    const path = [];
    for (let entry = frame; entry; entry = frames[entry.parent]) path.push(entry.name);
    return path.reverse().join(" → ");
  }

  function show(frame) {
    mode.textContent = frame === selected ? "Selected frame" : "Preview frame";
    for (const value of values) {
      const field = value.dataset.value;
      value.textContent = field === "path"
        ? namePath(frame)
        : frame.row.querySelector(`[data-cell=${field}]`)?.textContent ?? "Not available";
    }
  }

  function markTabStop(frame) {
    tabStop.button.tabIndex = -1;
    frame.button.tabIndex = 0;
    tabStop = frame;
  }

  function select(frame) {
    selected.button.setAttribute("aria-pressed", "false");
    selected = frame;
    frame.button.setAttribute("aria-pressed", "true");
    markTabStop(frame);
    zoom.disabled = frame === zoomRoot;
    show(frame);
  }

  function changeScale(root) {
    zoomRoot = root;
    reset.disabled = !root;
    let levels = 0;
    for (const frame of frames) {
      const button = frame.button;
      button.hidden = !!root && frame !== root && (frame.parent < 0 || frames[frame.parent].button.hidden);
      if (button.hidden) continue;
      const depth = frame.depth - (root?.depth ?? 0);
      const offset = root ? 100 * (frame.offset - root.offset) / root.share : frame.offset;
      const share = root ? 100 * frame.share / root.share : frame.share;
      button.style.setProperty("--left", `${Math.max(0, offset)}%`);
      button.style.setProperty("--width", `${Math.max(0, Math.min(100 - offset, share))}%`);
      button.style.setProperty("--depth", depth);
      levels = Math.max(levels, depth + 1);
    }
    plot.style.setProperty("--levels", root ? levels : fullLevels);
    scale.textContent = root ? `Scale: ${root.name} subtree` : "Scale: full recording";
  }

  function focus(frame) {
    if (frame && !frame.button.hidden) frame.button.focus();
  }

  function move(event, frame) {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    let next;
    switch (event.key) {
      case "ArrowUp":
        next = frames[frame.parent];
        break;
      case "ArrowDown":
        next = frames.find((entry) => frames[entry.parent] === frame && !entry.button.hidden);
        break;
      case "ArrowLeft":
      case "ArrowRight":
      case "Home":
      case "End": {
        const row = frames.filter((entry) => entry.depth === frame.depth && !entry.button.hidden);
        if (event.key === "Home") next = row[0];
        else if (event.key === "End") next = row[row.length - 1];
        else next = row[row.indexOf(frame) + (event.key === "ArrowLeft" ? -1 : 1)];
        break;
      }
      case "Escape":
        show(selected);
        return;
      default:
        return;
    }
    event.preventDefault();
    focus(next);
  }

  for (const frame of frames) {
    const { button } = frame;
    button.disabled = false;
    button.tabIndex = -1;
    button.addEventListener("focus", () => { markTabStop(frame); show(frame); });
    button.addEventListener("keydown", (event) => move(event, frame));
    button.addEventListener("pointerenter", (event) => {
      if (event.pointerType !== "touch") show(frame);
    });
    button.addEventListener("click", () => select(frame));
    const inspect = frame.row.querySelector("[data-inspect]");
    inspect.hidden = false;
    inspect.addEventListener("click", () => {
      if (button.hidden) changeScale(null);
      select(frame);
      focus(frame);
    });
  }
  layout.addEventListener("pointerleave", () => show(plot.contains(document.activeElement) ? tabStop : selected));
  plot.addEventListener("focusout", (event) => {
    if (!plot.contains(event.relatedTarget)) show(selected);
  });
  zoom.addEventListener("click", () => {
    changeScale(selected);
    select(selected);
    focus(selected);
  });
  reset.addEventListener("click", () => {
    changeScale(null);
    select(selected);
    focus(selected);
  });
  reset.disabled = true;
  select(selected);
  section.querySelector("[data-help]").hidden = false;
  scale.hidden = false;
  readout.hidden = false;
  layout.dataset.ready = "";
}
