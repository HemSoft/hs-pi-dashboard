const assert = require('node:assert/strict');
const { test } = require('node:test');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function renderer() {
  const html = readFileSync(path.join(__dirname, '../internal/web/index.html'), 'utf8');
  const start = html.indexOf('// Row signature tracks');
  const end = html.indexOf('let lastHermes =', start);
  assert.ok(start >= 0 && end > start, 'session renderer boundaries');
  const sandbox = vm.createContext({
    esc: (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])),
    compactDur: () => '1s', durationOf: () => '1s',
  });
  vm.runInContext(html.slice(start, end), sandbox);
  return sandbox;
}

function claude(overrides = {}) {
  return { id: 'claude-code:shared', project: 'dashboard', provider: 'claude-code', model: 'claude-opus-4-6', active: true, startedAt: new Date().toISOString(), lastActivity: new Date().toISOString(), ...overrides };
}

test('Claude and Pi rows coexist and active Claude is visible before output', () => {
  const r = renderer();
  const html = r.sessionsTable([{ name: 'home', online: true, sessions: [{ ...claude(), id: 'shared', provider: 'openai-codex' }, claude()] }], []);
  assert.match(html, /claude-code/);
  assert.match(html, /openai-codex/);
  assert.equal((html.match(/<tr class="active"/g) || []).length, 2);
});

test('Claude output can expand under its namespaced row and is escaped', () => {
  const r = renderer();
  vm.runInContext('expandedOutputs.add("home/claude-code:shared")', r);
  const html = r.sessionsTable([{ name: 'home', online: true, sessions: [claude({ outputs: [{ text: '<script>unsafe</script>', at: new Date().toISOString() }] })] }], []);
  assert.match(html, /data-skey="home\/claude-code:shared"/);
  assert.match(html, /&lt;script&gt;unsafe&lt;\/script&gt;/);
  assert.doesNotMatch(html, /<script>/);
});

test('offline Claude with no output does not appear as live work', () => {
  const r = renderer();
  const html = r.sessionsTable([{ name: 'home', online: false, sessions: [claude()] }], []);
  assert.doesNotMatch(html, /<tr class="active"/);
  assert.match(html, /no active sessions or sessions with output yet/);
});
