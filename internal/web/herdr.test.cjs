const {test} = require('node:test');
const assert = require('node:assert/strict');
const {rows,rowHTML,terminalURL} = require('./herdr.js');
const fs = require('node:fs');
const vm = require('node:vm');
function view() {
  const nodes = Object.fromEntries(['herdr-machines','herdr-machine-filter','herdr-state-filter','herdr-summary','herdr-rows','herdr-note'].map(id => [id,{
    value:'all', innerHTML:'', textContent:'', options:['all','home','air','mini'].map(value => ({value})),
    classList:{toggle() {}, add() {}}, addEventListener(event, callback) { this[event] = callback; },
  }]));
  const context = {window:{},document:{getElementById:id => nodes[id],querySelectorAll:() => []},URL,Date};
  vm.runInNewContext(fs.readFileSync(require.resolve('./herdr.js'),'utf8'),context);
  return {nodes, ui:context.window.HerdrDashboard};
}
const now = Date.now();
const machine = (name, online=true, generatedAt=new Date(now).toISOString()) => ({name,online,terminalUrl:`https://${name}.hemsoft.net`,herdr:{available:true,generatedAt,sessions:[{name:'default',agents:[{agent:'pi',state:'working',workspace:'Dashboard',paneId:'w1:p1',cwd:'/work/project'},{agent:'codex',state:'blocked',workspace:'API',paneId:'w1:p2'}]}]}});
test('counts only live, fresh, successfully queried Herdr agents', () => {
  const fleet = {machines:[machine('home'),machine('offline',false),machine('stale',true,'2020-01-01T00:00:00Z'),{name:'missing',online:true}]};
  assert.equal(rows(fleet,'all','all',now).length,2);
  assert.equal(rows(fleet,'all','all',now)[0].state,'blocked');
});
test('filters machine and state without changing source data', () => {
  const fleet = {machines:[machine('home'),machine('air')]};
  assert.equal(rows(fleet,'air','blocked',now).length,1);
  assert.equal(rows(fleet,'mini','all',now).length,0);
  assert.equal(fleet.machines[0].herdr.sessions[0].agents.length,2);
});
test('failed sessions and unavailable collectors never count as live', () => {
  const m = machine('home'); m.herdr.sessions[0].error='unreachable';
  assert.equal(rows({machines:[m]},'all','all',now).length,0);
  delete m.herdr.sessions[0].error; m.herdr.available=false;
  assert.equal(rows({machines:[m]},'all','all',now).length,0);
});
test('unknown lifecycle states remain unknown', () => {
  const m = machine('home'); m.herdr.sessions[0].agents[0].state='future-state';
  assert.equal(rows({machines:[m]},'all','unknown',now)[0].state,'unknown');
});
test('escapes agent and workspace metadata instead of executing it', () => {
  const html=rowHTML({state:'working',workspace:'<img src=x onerror=alert(1)>',cwd:'" onmouseover="x',machine:'<script>',paneId:'w1:p1',agent:'pi',session:'default'});
  assert.ok(!html.includes('<img'));
  assert.ok(!html.includes('<script>'));
  assert.ok(html.includes('&lt;img'));
});
test('readout follows the machine filter and keeps state counts visible', () => {
  const {nodes,ui} = view();
  ui.render({machines:[machine('home'),machine('air')]});
  assert.match(nodes['herdr-summary'].textContent,/2 working · 2 blocked/);
  nodes['herdr-machine-filter'].value='air';
  nodes['herdr-machine-filter'].change();
  assert.match(nodes['herdr-summary'].textContent,/1 working · 1 blocked/);
  nodes['herdr-state-filter'].value='blocked';
  nodes['herdr-state-filter'].change();
  assert.equal((nodes['herdr-rows'].innerHTML.match(/class="herdr-row"/g)||[]).length,1);
  assert.match(nodes['herdr-summary'].textContent,/1 working · 1 blocked/);
});
test('changing filters after an outage cannot resurrect cached live rows', () => {
  const {nodes,ui} = view();
  ui.render({machines:[machine('home')]});
  ui.fail();
  nodes['herdr-machine-filter'].change();
  assert.ok(!nodes['herdr-rows'].innerHTML.includes('class="herdr-row"'));
  assert.match(nodes['herdr-note'].textContent,/connection lost/);
  assert.match(nodes['herdr-summary'].textContent,/Live states unavailable/);
});
test('terminal links accept HTTPS only and no embedded credentials', () => {
  assert.equal(terminalURL('https://home.hemsoft.net'),'https://home.hemsoft.net/');
  for(const u of ['javascript:alert(1)','http://home','https://user:secret@home','not-a-url']) assert.equal(terminalURL(u),'');
});
