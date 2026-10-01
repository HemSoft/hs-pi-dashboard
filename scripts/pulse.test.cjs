// Exercise the embedded chart renderer, including display-scale changes.
const assert = require('node:assert/strict');
const { test } = require('node:test');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function monitor(dpr) {
  const window = { devicePixelRatio: dpr };
  const sandbox = vm.createContext({ window, setInterval: () => 1, clearInterval() {} });
  vm.runInContext(readFileSync(path.join(__dirname, '../internal/web/vendor/smoothie-1.36.1.js'), 'utf8'), sandbox);
  let scale = 1;
  let points = [];
  let trace = [];
  const context = new Proxy({
    scale(x) { scale *= x; },
    beginPath() { points = []; },
    moveTo(x, y) { points.push([x * scale, y * scale]); },
    lineTo(x, y) { points.push([x * scale, y * scale]); },
    stroke() { if (this.strokeStyle === '#32ff7e') trace = points.slice(); },
  }, { get(target, key) { return target[key] ?? (() => {}); } });
  const canvas = {
    offsetWidth: 960, offsetHeight: 182, width: 960, height: 182,
    getContext: () => context,
    setAttribute(key, value) { this[key] = Number(value); scale = 1; },
  };
  const chart = new sandbox.SmoothieChart({
    responsive: true, millisPerPixel: 20, scaleSmoothing: 1,
    yRangeFunction: () => ({ min: -.175, max: 5.175 }),
    labels: { disabled: true }, grid: { verticalSections: 0, borderVisible: false },
  });
  const series = new sandbox.TimeSeries();
  series.append(1800000000000 - 60000, 2);
  series.append(1800000000000, 2);
  chart.addTimeSeries(series, { strokeStyle: '#32ff7e', lineWidth: 2.5, interpolation: 'linear' });
  chart.canvas = canvas;
  chart.delay = -4800;
  return { window, canvas, chart, trace: () => trace };
}

for (const [before, after, changeWidth] of [
  [1, 1.25, true], [1.25, 1.5, true], [2, 1, true],
  [1, 2, false], [2, 1.25, false],
]) {
  test(`Pulse trace stays aligned at DPR ${before} -> ${after}, width change ${changeWidth}`, () => {
    const m = monitor(before);
    m.chart.render(m.canvas, 1800000000000);
    m.window.devicePixelRatio = after;
    if (changeWidth) m.canvas.offsetWidth += 20;
    m.chart.render(m.canvas, 1800000001000);
    assert.equal(m.canvas.width, Math.floor(m.canvas.offsetWidth * after), 'backing width');
    assert.equal(m.canvas.height, Math.floor(m.canvas.offsetHeight * after), 'backing height');
    const traceY = m.trace().at(-1)[1] * m.canvas.offsetHeight / m.canvas.height;
    const cursorY = m.canvas.offsetHeight * (1 - (2 - m.chart.currentVisMinValue) / m.chart.currentValueRange);
    assert.ok(Math.abs(traceY - cursorY) < 1, `trace ${traceY}, cursor ${cursorY}`);
  });
}
