(function (root) {
  'use strict';
  const states = ['blocked', 'done', 'working', 'idle', 'unknown'];
  const escape = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;'}[c]));
  function terminalURL(value) {
    try { const u = new URL(value); return u.protocol === 'https:' && !u.username && !u.password ? u.href : ''; }
    catch { return ''; }
  }
  function rows(fleet, machine = 'all', state = 'all', now = Date.now()) {
    const out = [];
    for (const m of fleet.machines || []) {
      if (!m.online || !m.herdr?.available || machine !== 'all' && machine !== m.name) continue;
      const age = now - Date.parse(m.herdr.generatedAt);
      if (!Number.isFinite(age) || age > 20000 || age < -60000) continue;
      for (const session of m.herdr.sessions || []) {
        if (session.error) continue;
        for (const agent of session.agents || []) {
          const status = states.includes(agent.state) ? agent.state : 'unknown';
          if (state !== 'all' && state !== status) continue;
          out.push({...agent, state:status, machine:m.name, session:session.name});
        }
      }
    }
    return out.sort((a,b) => states.indexOf(a.state)-states.indexOf(b.state) || a.machine.localeCompare(b.machine) || (a.workspace || '').localeCompare(b.workspace || ''));
  }
  function rowHTML(a) {
    const project = a.workspace || a.cwd?.split(/[\\/]/).filter(Boolean).pop() || 'Untitled workspace';
    return `<li class="herdr-row"><span class="herdr-project">${escape(project)}<span class="herdr-path" title="${escape(a.cwd)}">${escape(a.cwd)}</span></span><span class="herdr-location">${escape(a.machine)}<small>${escape(a.paneId)}</small></span><span class="herdr-agent">${escape(a.name || a.agent || 'agent')}</span><span class="herdr-session">${escape(a.session)}</span><span class="herdr-state ${a.state}">${escape(a.state)}</span></li>`;
  }
  let latest = null;
  function render(fleet) {
    latest = fleet;
    const container = document.getElementById('herdr-machines');
    const machines = (fleet.machines || []).filter(m => m.terminalUrl);
    const arrow = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M5 12h14M13 6l6 6-6 6"/></svg>';
    const html = machines.map(m => {
      const live = rows({machines:[m]}).length;
      const href = m.online ? terminalURL(m.terminalUrl) : '';
      const detail = !m.online ? 'Collector offline' : !m.herdr?.available ? 'Live states unavailable' : `${live} agent${live === 1 ? '' : 's'} in Herdr`;
      const contents = `<span><strong>${escape(m.name)}</strong><small>${escape(detail)}${href ? ' · open terminal' : ''}</small></span>${href ? arrow : ''}`;
      return href ? `<a class="herdr-machine" href="${escape(href)}" target="_blank" rel="noopener noreferrer" aria-label="Open ${escape(m.name)} terminal in a new tab">${contents}</a>` : `<span class="herdr-machine unavailable">${contents}</span>`;
    }).join('');
    if (container.innerHTML !== html) container.innerHTML = html;
    const selector = document.getElementById('herdr-machine-filter');
    const wanted = ['all', ...machines.map(m => m.name)];
    if ([...selector.options].map(o => o.value).join('|') !== wanted.join('|')) {
      const selected = selector.value;
      selector.innerHTML = '<option value="all">All machines</option>' + machines.map(m => `<option value="${escape(m.name)}">${escape(m.name)}</option>`).join('');
      if (wanted.includes(selected)) selector.value = selected;
    }
    renderRows();
  }
  function renderRows() {
    if (!latest) return;
    const agents = rows(latest, document.getElementById('herdr-machine-filter').value, document.getElementById('herdr-state-filter').value);
    const all = rows(latest, document.getElementById('herdr-machine-filter').value);
    const count = state => all.filter(a => a.state === state).length;
    const summary = `${count('working')} working · ${count('blocked')} blocked · ${count('done')} done · ${count('idle')} idle · ${count('unknown')} unknown`;
    const readout = document.getElementById('herdr-summary');
    if (readout.textContent !== summary) readout.textContent = summary;
    const html = agents.map(rowHTML).join('') || '<li class="herdr-empty">No live Herdr agents match this view. Start an agent in a Herdr pane to see its state here.</li>';
    const element = document.getElementById('herdr-rows');
    if (element.innerHTML !== html) element.innerHTML = html;
    const issues = (latest.machines || []).filter(m => m.terminalUrl && (!m.online || !m.herdr?.available || m.herdr?.error || (m.herdr?.sessions || []).some(s => s.error)));
    const note = document.getElementById('herdr-note');
    note.classList.toggle('error', issues.length > 0);
    note.textContent = issues.length ? issues.map(m => `${m.name}: ${m.herdr?.error || (!m.online ? 'collector offline' : 'some sessions could not be read')}`).join('. ') : 'Live states come from Herdr. Work still running in tmux is not included in these counts. Session history and usage remain below.';
  }
  function fail() {
    latest = null;
    const note = document.getElementById('herdr-note');
    note.classList.add('error');
    note.textContent = 'Dashboard connection lost. Live states are unavailable; retrying automatically.';
    document.getElementById('herdr-summary').textContent = 'Live states unavailable';
    document.getElementById('herdr-rows').innerHTML = '<li class="herdr-empty">Cannot load live agent states.</li>';
    document.querySelectorAll('#herdr-machines a').forEach(a => a.removeAttribute('href'));
  }
  root.HerdrDashboard = {render, fail, rows, rowHTML, terminalURL};
  if (typeof module !== 'undefined') module.exports = root.HerdrDashboard;
  if (typeof document !== 'undefined') {
    document.getElementById('herdr-machine-filter').addEventListener('change', renderRows);
    document.getElementById('herdr-state-filter').addEventListener('change', renderRows);
  }
})(typeof window === 'undefined' ? globalThis : window);
