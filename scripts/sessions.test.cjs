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

function t3(overrides = {}) {
  return { ...claude(), id: 't3:env:thread', source: 't3', name: 'Fix <unsafe> title', provider: 'codex', status: 'idle', active: false, project: 'fleet-repo', toolCallCount: 7, ...overrides };
}

test('quiet T3 threads remain visible with status, repository and unknown metrics', () => {
  const r = renderer();
  const html = r.sessionsTable([{ name: 'mini', online: true, sessions: [t3()] }], []);
  assert.match(html, /Fix &lt;unsafe&gt; title/);
  assert.match(html, /T3 idle/);
  assert.match(html, /fleet-repo/);
  assert.match(html, /<b>tools<\/b> 7/);
  assert.match(html, /<b>in<\/b> n\/a/);
  assert.match(html, /<b>context<\/b> n\/a \/ n\/a/);
  assert.doesNotMatch(html, /class="offline"/);
});

test('T3 reported zero is distinct from missing, and context is not total tokens', () => {
  const r = renderer();
  const html = r.sessionsTable([{ name: 'mini', online: true, sessions: [t3({t3Usage: {inputTokens: 1200, outputTokens: 0, totalTokens: null, contextTokens: 45, contextLimit: 200}})] }], []);
  assert.match(html, /<b>in<\/b> 1,200/);
  assert.match(html, /<b>out<\/b> 0/);
  assert.match(html, /<b>total<\/b> n\/a/);
  assert.match(html, /<b>context<\/b> 45 \/ 200/);
});

test('source failure and offline cached T3 metadata are visible without live rows', () => {
  const r = renderer();
  const machine = { name: 'mini', online: false, sources: [{source: 't3', state: 'healthy'}], sessions: [t3({active: true, status: 'working'})] };
  let html = r.sessionsTable([machine], []);
  assert.match(html, /T3 offline/);
  assert.match(html, /Cached sessions; machine unreachable/);
  assert.doesNotMatch(html, /class="active"/);
  html = r.sessionsTable([{name: 'air', online: true, sources: [{source: 't3', state: 'unavailable', error: '<database missing>'}], sessions: []}], []);
  assert.match(html, /T3 unavailable/);
  assert.match(html, /&lt;database missing&gt;/);
});

test('T3 state and usage updates flash and trigger Pulse only while live', () => {
  const r = renderer();
  const m = { name: 'mini', online: true, sessions: [t3({ active: true, status: 'working', t3Usage: {inputTokens: 1}})] };
  r.sessionsTable([m], []);
  vm.runInContext('sessionBaselineReady = true', r);
  m.sessions[0].t3Usage.inputTokens = 2;
  assert.match(r.sessionsTable([m], []), /class="active flash"/);
  assert.equal(vm.runInContext('activeFlashDetected', r), true);
  vm.runInContext('activeFlashDetected = false', r);
  m.sessions[0].active = false;
  m.sessions[0].status = 'stopped';
  r.sessionsTable([m], []);
  assert.equal(vm.runInContext('activeFlashDetected', r), false);
});

test('T3 duration uses the current or latest turn and stops growing offline', () => {
  const r = renderer();
  r.compactDur = ms => String(ms) + 'ms';
  const start = new Date(Date.now() - 10000).toISOString();
  const end = new Date(Date.now() - 5000).toISOString();
  const s = t3({turnStartedAt: start, turnCompletedAt: end});
  assert.equal(r.t3TurnDuration(s, true), '5s');
  assert.equal(r.t3TurnDuration(t3(), true), 'n/a');
  assert.equal(r.t3TurnDuration({...s, turnCompletedAt: null, lastActivity: end, active: true}, false), '5s');
});

test('Pulse counts working/waiting T3 rows and excludes offline or stale rows', () => {
  const html = readFileSync(path.join(__dirname, '../internal/web/index.html'), 'utf8');
  const start = html.indexOf('function snapshotActiveSessionCount(');
  const end = html.indexOf('function cardCollapsed(', start);
  const r = vm.createContext({});
  vm.runInContext(html.slice(start,end),r);
  assert.equal(r.snapshotActiveSessionCount({machines: [
    {online:true,sessions:[t3({active:true,status:'working'}),t3({active:true,status:'waiting'}),t3({status:'stale'})]},
    {online:false,sessions:[t3({active:true,status:'working'})]}],hermesSessions:[]}),2);
});

test('T3 notices identify and escape each configured environment path', () => {
  const r=renderer();
  const html=r.sessionsTable([{name:'mini',online:true,sessions:[],sources:[
    {source:'t3',state:'unavailable',error:'unreadable',path:'/first/<unsafe>/state.sqlite'},
    {source:'t3',state:'unavailable',error:'unreadable',path:'/second/state.sqlite'}]}],[]);
  assert.match(html,/\/first\/&lt;unsafe&gt;\/state.sqlite/);
  assert.match(html,/\/second\/state.sqlite/);
});

test('independent live Claude fallback identifies cached T3 statistics and unknown turn time', () => {
  const r=renderer();
  const html=r.sessionsTable([{name:'mini',online:true,sessions:[t3({active:true,status:'unavailable',activitySource:'claude-code'})]}],[]);
  assert.match(html,/T3 unavailable/);
  assert.match(html,/Claude transcript active/);
  assert.match(html,/<b>statistics<\/b> cached T3/);
  assert.match(html,/<b>turn<\/b> n\/a/);
});
