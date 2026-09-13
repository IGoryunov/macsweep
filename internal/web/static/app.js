// macsweep web UI: talks only to the local server that opened this page.
(() => {
  const token = new URLSearchParams(location.search).get('token') || '';
  const H = { 'X-Token': token };
  const $ = id => document.getElementById(id);
  const fmt = b => b >= 2 ** 30 ? (b / 2 ** 30).toFixed(1) + ' GB' : b >= 2 ** 20 ? Math.round(b / 2 ** 20) + ' MB' : b >= 1024 ? Math.round(b / 1024) + ' KB' : b + ' B';

  let report = null;          // last /api/candidates
  let candidates = new Map(); // path -> candidate
  let tree = null;            // current subtree root from /api/tree
  let fullMap = false;
  let marked = new Map();     // path -> candidate
  let hover = null;

  // ---- heartbeat: the server exits when we stop sending ----
  setInterval(() => fetch('/api/heartbeat', { method: 'POST', headers: H }).catch(() => {}), 5000);
  fetch('/api/heartbeat', { method: 'POST', headers: H });

  // ---- SSE jobs ----
  function job(url, onDone) {
    $('progress').hidden = false;
    const es = new EventSource(url + (url.includes('?') ? '&' : '?') + 'token=' + token);
    es.addEventListener('progress', e => {
      const p = JSON.parse(e.data);
      $('progress-text').textContent = `${p.paths.toLocaleString()} paths, ${fmt(p.bytes)}  ${p.current || ''}`;
    });
    es.addEventListener('done', () => { es.close(); $('progress').hidden = true; onDone(); });
    es.addEventListener('error', e => {
      es.close(); $('progress').hidden = true;
      if (e.data) $('totals').textContent = 'error: ' + JSON.parse(e.data).error;
    });
  }

  async function loadCandidates() {
    const r = await fetch('/api/candidates', { headers: H });
    report = await r.json();
    candidates = new Map();
    for (const g of report.groups) for (const c of g.candidates) { c.group = g.name; candidates.set(c.path, c); }
    for (const p of [...marked.keys()]) if (!candidates.has(p)) marked.delete(p);
    const t = report.totals;
    $('totals').textContent = `✓ ${fmt(t.safe_bytes)} safe · ? ${fmt(t.review_bytes)} review · ${fmt(t.keep_bytes)} keep` + (report.from_cache ? ' · cached sizes' : '');
    $('rescan').disabled = false; $('fullmap').disabled = false;
    renderGroups(); renderMarked();
    if (fullMap) await loadTree(tree ? tree.path : null);
    else buildMap();
  }

  function buildMap() {
    $('fullmap').disabled = true;
    $('crumbs').textContent = 'mapping every directory under ~ … (the list below is already usable)';
    job('/api/tree/build', async () => { await loadTree(null); $('fullmap').disabled = false; });
  }
  let homeBytes = 0, homeFiles = 0, diskTotal = 0, diskFree = 0;

  async function loadTree(path) {
    const q = new URLSearchParams({ depth: 5, limit: 64 });
    if (path) q.set('path', path);
    const r = await fetch('/api/tree?' + q, { headers: H });
    if (!r.ok) { if (path) return loadTree(null); return; }
    const d = await r.json();
    fullMap = d.full; tree = d.root; homeBytes = d.home_bytes; homeFiles = d.home_files; diskTotal = d.disk_total || 0; diskFree = d.disk_free || 0;
    draw();
  }

  $('rescan').onclick = () => { $('rescan').disabled = true; job('/api/scan?rescan=1', loadCandidates); };
  $('fullmap').onclick = buildMap;
  $('onlyverdicts').onchange = draw;

  // ---- sunburst (plain SVG) ----
  const svg = $('sunburst');
  const R0 = 70, RMAX = 310, DEPTH = 5;
  function verdictOf(node, inherited) { return node.verdict || inherited || ''; }
  function color(node, inherited, depth, index, siblings) {
    const v = verdictOf(node, inherited);
    if (v === 'safe') return 'var(--safe)';
    if (v === 'review') return 'var(--review)';
    if (v === 'keep') return 'var(--keep)';
    const hue = 200 + (index / Math.max(siblings, 1)) * 120;
    return `hsl(${hue} 35% ${62 - depth * 7}%)`;
  }
  function arc(r0, r1, a0, a1) {
    const large = a1 - a0 > Math.PI ? 1 : 0;
    const p = (r, a) => [r * Math.sin(a), -r * Math.cos(a)];
    const [x0, y0] = p(r1, a0), [x1, y1] = p(r1, a1), [x2, y2] = p(r0, a1), [x3, y3] = p(r0, a0);
    return `M${x0},${y0}A${r1},${r1},0,${large},1,${x1},${y1}L${x2},${y2}A${r0},${r0},0,${large},0,${x3},${y3}Z`;
  }
  function draw() {
    if (!tree) return;
    const only = $('onlyverdicts').checked;
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    const band = (RMAX - R0) / DEPTH;
    const total = tree.bytes || 1;
    const centre = document.createElementNS(svg.namespaceURI, 'circle');
    centre.setAttribute('r', R0 - 4); centre.setAttribute('class', 'center');
    centre.onclick = () => zoomOut();
    centre.onmouseenter = () => showNode(tree, tree.verdict);
    svg.appendChild(centre);
    const text = (y, size, str, fill) => {
      const el = document.createElementNS(svg.namespaceURI, 'text');
      el.setAttribute('text-anchor', 'middle'); el.setAttribute('y', y); el.setAttribute('fill', fill || 'var(--fg)'); el.setAttribute('font-size', size);
      el.textContent = str; svg.appendChild(el);
    };
    const name = tree.name.length > 14 ? tree.name.slice(0, 13) + '…' : tree.name;
    text(-6, 12, name);
    text(12, 14, fmt(tree.bytes));
    if (tree.path !== (report && report.home)) text(28, 10, '↑ click to go up', 'var(--dim)');
    const MIN = 0.004;
    const sector = (depth, a, b, fill, opacity, title, onEnter, onClick) => {
      const path = document.createElementNS(svg.namespaceURI, 'path');
      path.setAttribute('d', arc(R0 + (depth - 1) * band, R0 + depth * band - 1, a, b));
      path.setAttribute('fill', fill);
      if (opacity) path.setAttribute('opacity', opacity);
      if (depth >= 3) path.setAttribute('stroke-width', '0.5');
      path.onmouseenter = onEnter; path.onclick = onClick;
      const t = document.createElementNS(svg.namespaceURI, 'title'); t.textContent = title; path.appendChild(t);
      svg.appendChild(path);
    };
    const walk = (node, depth, a0, a1, inherited) => {
      if (depth > DEPTH || !node.children) return;
      let a = a0;
      const span = a1 - a0;
      const total = node.bytes || 1;
      const kids = node.children.slice().sort((x, y) => y.bytes - x.bytes);
      let tiny = 0, tinyN = 0, kidsSum = 0;
      kids.forEach((k, i) => {
        kidsSum += k.bytes;
        const w = span * (k.bytes / total);
        const v = verdictOf(k, inherited);
        if (w <= MIN) { tiny += k.bytes; tinyN++; return; } // merged below, no gap
        const b = a + w;
        if (!only || v) {
          sector(depth, a, b, color(k, inherited, depth, i, kids.length), only && !v ? '0.15' : k.skipped ? '0.35' : '',
            `${k.name} — ${fmt(k.bytes)}${v ? ' — ' + v : ''}${k.skipped ? ' — not mapped (cloud storage or another volume)' : ''}`,
            () => showNode(k, inherited),
            () => { if (k.children || fullMap) zoomTo(k.path); else showNode(k, inherited, true); });
        }
        walk(k, depth + 1, a, b, v);
        a = b;
      });
      // Small children (drawn individually they would be hairlines) plus the
      // ones the server folded: one grey sector, zoomable.
      const small = tiny + (node.other || 0), smallN = tinyN + (node.other_n || 0);
      if (small > 0 && !only) {
        const b = a + span * (small / total);
        if (b - a > 0.0005) sector(depth, a, b, 'var(--other)', '', `${smallN} smaller items — ${fmt(small)} (click to zoom in)`,
          () => showOther({ path: node.path, other: small, other_n: smallN }), () => zoomTo(node.path));
        a = b;
      }
      // Files stored directly in this directory: the part no child explains.
      const loose = total - kidsSum - (node.other || 0);
      if (loose > 0 && !only) {
        const b = a + span * (loose / total);
        if (b - a > 0.0005) sector(depth, a, b, 'var(--loose)', '', `files directly in ${node.name} — ${fmt(loose)}`,
          () => showLoose(node, loose), () => zoomTo(node.path));
      }
    };
    walk(tree, 1, 0, 2 * Math.PI, tree.verdict);
    renderCrumbs();
  }
  function zoomTo(path) { loadTree(path); }
  function zoomOut() {
    if (!tree || tree.path === report.home) return;
    const parent = tree.path.slice(0, tree.path.lastIndexOf('/')) || '/';
    loadTree(parent);
  }
  function renderCrumbs() {
    const home = report ? report.home : '';
    const rel = tree.path.startsWith(home) ? tree.path.slice(home.length) : tree.path;
    const parts = rel.split('/').filter(Boolean);
    const nav = $('crumbs'); nav.innerHTML = '';
    const mk = (text, path) => { const a = document.createElement('a'); a.textContent = text; a.onclick = () => loadTree(path); return a; };
    nav.appendChild(mk('~', home));
    let acc = home;
    for (const p of parts) { acc += '/' + p; nav.appendChild(document.createTextNode(' / ')); nav.appendChild(mk(p, acc)); }
    const s = document.createElement('span');
    let txt = `   — ${fmt(tree.bytes)} here, ${fmt(homeBytes)} in ~ (${homeFiles.toLocaleString()} files)`;
    if (diskTotal) {
      const used = diskTotal - diskFree;
      txt += `. Disk: ${fmt(used)} used of ${fmt(diskTotal)}, ${fmt(diskFree)} free; ~ is ${Math.round(homeBytes / used * 100)}% of what is used, the rest is the system, applications and other users, which macsweep never touches.`;
    }
    s.textContent = txt;
    nav.appendChild(s);
  }

  // ---- side panel ----
  function showNode(node, inherited, pin) {
    hover = node;
    const c = candidates.get(node.path) || (inherited && findOwner(node.path));
    $('p-name').textContent = node.name || node.path;
    $('p-path').textContent = display(node.path);
    $('p-size').textContent = node.skipped ? 'not mapped: cloud storage or another volume, contents are not on this disk' : `${fmt(node.bytes)}${node.files ? ` in ${node.files.toLocaleString()} files` : ''}`;
    const v = c ? c.verdict : '';
    $('p-verdict').innerHTML = v ? `<span class="v-${v}">${{ safe: '✓ safe', review: '? review', keep: '– keep' }[v]}</span> · rule ${c.rule_id}${c.path !== node.path ? ' (inherited from ' + display(c.path) + ')' : ''}${c.blocked ? ' · <b>' + c.blocked + '</b>' : ''}` : 'no rule covers this path';
    $('p-note').textContent = c ? c.note : '';
    if (c && c.tags) {
      const t = c.tags, facts = [];
      if (t.downloaded) facts.push(`downloaded ${t.downloaded}${t.source ? ' from ' + t.source : ''} (${t.age_bucket})`);
      if (t.last_used) facts.push(`${t.app} last launched ${t.last_used}, ${t.unused_days} days ago`);
      if (t.owner) facts.push(`no installed application matches “${t.owner}”`);
      if (t.command) facts.push(`command: ${t.command}`);
      if (facts.length) $('p-note').textContent += ' — ' + facts.join(' · ');
    }
    $('p-recovery').textContent = c && c.recovery ? 'recovery: ' + c.recovery : '';
    const ul = $('p-reasons'); ul.innerHTML = '';
    if (c) for (const r of c.reasons) { const li = document.createElement('li'); li.textContent = r; ul.appendChild(li); }
    const own = c && c.path === node.path;
    const mark = $('p-mark');
    mark.hidden = !(own && c.verdict !== 'keep' && !c.blocked);
    mark.textContent = marked.has(node.path) ? 'Unmark' : 'Mark for Trash';
    mark.onclick = () => { toggleMark(c); showNode(node, inherited); };
    $('p-explain').hidden = node.path.includes('://');
    $('p-explain').onclick = () => explain(node.path);
    $('p-explanation').hidden = true;
  }
  function showLoose(node, bytes) {
    $('p-name').textContent = `files directly in ${node.name}`;
    $('p-path').textContent = display(node.path);
    $('p-size').textContent = `${fmt(bytes)} in files that sit in this directory itself rather than in a subdirectory`;
    $('p-verdict').textContent = ''; $('p-note').textContent = ''; $('p-recovery').textContent = '';
    $('p-reasons').innerHTML = ''; $('p-mark').hidden = true; $('p-explain').hidden = true; $('p-explanation').hidden = true;
  }
  function showOther(node) {
    $('p-name').textContent = `${node.other_n} smaller items`;
    $('p-path').textContent = display(node.path);
    $('p-size').textContent = `${fmt(node.other)} in directories too small to draw individually; click to zoom in`;
    $('p-verdict').textContent = ''; $('p-note').textContent = ''; $('p-recovery').textContent = '';
    $('p-reasons').innerHTML = ''; $('p-mark').hidden = true; $('p-explain').hidden = true; $('p-explanation').hidden = true;
  }
  function findOwner(path) { for (const [p, c] of candidates) if (path.startsWith(p + '/')) return c; return null; }
  function display(p) { return report && p.startsWith(report.home) ? '~' + p.slice(report.home.length) : p; }
  async function explain(path) {
    const r = await fetch('/api/explain?' + new URLSearchParams({ path }), { headers: H });
    const ex = await r.json();
    const lines = [`policy: ${ex.allowed ? 'allowed' : 'DENIED — ' + ex.policy}`];
    for (const m of ex.matches || []) {
      lines.push(`\nrule ${m.rule_id} (${m.group}) matched by ${m.matched_by}`);
      for (const p of m.predicates || []) lines.push(`  ${p.expr}: ${p.error ? 'error: ' + p.error : p.result}`);
      lines.push(`  verdict: ${m.verdict}`);
      for (const rs of m.reasons || []) lines.push(`    - ${rs}`);
    }
    if (!(ex.matches || []).length) lines.push('no rule matches this path');
    const pre = $('p-explanation'); pre.textContent = lines.join('\n'); pre.hidden = false;
  }

  // ---- marking and trashing ----
  function toggleMark(c) {
    if (!c || c.verdict === 'keep' || c.blocked) return;
    if (marked.has(c.path)) marked.delete(c.path); else marked.set(c.path, c);
    renderMarked(); renderGroups();
  }
  function renderMarked() {
    const ul = $('m-list'); ul.innerHTML = '';
    let total = 0, review = 0;
    for (const c of marked.values()) {
      if (!(c.nested_in && marked.has(c.nested_in))) total += c.bytes;
      if (c.verdict === 'review') review++;
      const li = document.createElement('li');
      li.innerHTML = `<span class="v-${c.verdict}">${c.verdict === 'safe' ? '✓' : '?'}</span> <span class="size">${fmt(c.bytes)}</span> <span>${display(c.path)}</span>`;
      const b = document.createElement('button'); b.textContent = 'unmark'; b.onclick = () => toggleMark(c);
      li.appendChild(b); ul.appendChild(li);
    }
    $('m-total').textContent = marked.size ? `— ${marked.size} item(s), ${fmt(total)}${review ? `, ${review} review` : ''}` : '';
    $('m-trash').disabled = marked.size === 0;
    $('m-trash').className = marked.size ? 'primary' : '';
  }
  $('m-trash').onclick = async () => {
    const items = [...marked.values()];
    const review = items.filter(c => c.verdict === 'review');
    const total = items.filter(c => !(c.nested_in && marked.has(c.nested_in))).reduce((s, c) => s + c.bytes, 0);
    if (!confirm(`Move ${items.length} item(s), ${fmt(total)}, to the Trash?\n\nEverything goes to the Finder Trash; Put Back restores it. Each item is re-checked right before moving.`)) return;
    if (review.length && !confirm(`${review.length} of them are ? review items — meaningful data, not caches:\n\n${review.slice(0, 8).map(c => display(c.path)).join('\n')}${review.length > 8 ? '\n…' : ''}\n\nMove them too?`)) return;
    $('m-trash').disabled = true; $('m-result').textContent = 'moving…';
    const body = { items: items.map(c => ({ path: c.path, dev: c.dev, ino: c.ino, root_mod_time: c.root_mod_time })) };
    const r = await fetch('/api/trash', { method: 'POST', headers: { ...H, 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    if (!r.ok) { $('m-result').textContent = 'error: ' + await r.text(); $('m-trash').disabled = false; return; }
    const j = await r.json();
    $('m-result').textContent = `Moved ${fmt(j.trashed_bytes)} in ${j.trashed} item(s); skipped ${j.skipped}, failed ${j.failed}. ` +
      j.items.filter(o => o.status === 'skipped' || o.status === 'failed').map(o => `${display(o.path)}: ${o.reason}`).join(' · ');
    marked.clear();
    await loadCandidates();
  };
  const collapsed = new Set();
  const AGE_ORDER = ['> 1 year', '180–365 days', '90–180 days', '30–90 days', '< 30 days'];
  const isDocker = g => g.name.startsWith('Docker (advisor)');

  function renderGroups() {
    const box = $('groups'); box.innerHTML = '';
    if (!report) return;
    for (const g of report.groups) {
      const sec = document.createElement('section'); sec.className = 'grp';
      const h = document.createElement('div'); h.className = 'group';
      const fold = document.createElement('span'); fold.className = 'fold'; fold.textContent = collapsed.has(g.name) ? '▸' : '▾';
      h.appendChild(fold);
      const title = document.createElement('span');
      title.textContent = ` ${g.name} — ${g.candidates.length} item(s) · ✓ ${fmt(g.safe_bytes)} · ? ${fmt(g.review_bytes)}${g.keep_bytes ? ' · keep ' + fmt(g.keep_bytes) : ''}`;
      h.appendChild(title);
      h.onclick = () => { collapsed.has(g.name) ? collapsed.delete(g.name) : collapsed.add(g.name); renderGroups(); };
      const safe = g.candidates.filter(c => c.verdict === 'safe' && !c.blocked);
      if (safe.length) {
        const all = safe.every(c => marked.has(c.path));
        const b = document.createElement('button'); b.className = 'small';
        b.textContent = all ? `unmark ${safe.length} safe` : `mark all ${safe.length} safe`;
        b.onclick = e => { e.stopPropagation(); for (const c of safe) { if (all) marked.delete(c.path); else marked.set(c.path, c); } renderMarked(); renderGroups(); };
        h.appendChild(b);
      }
      sec.appendChild(h);
      if (collapsed.has(g.name)) { box.appendChild(sec); continue; }
      if (isDocker(g)) { sec.appendChild(renderDocker(g)); box.appendChild(sec); continue; }
      if (g.name === 'Downloads') {
        for (const bucket of AGE_ORDER) {
          const items = g.candidates.filter(c => (c.tags || {}).age_bucket === bucket);
          if (!items.length) continue;
          const sub = document.createElement('div'); sub.className = 'subgroup';
          const total = items.reduce((s, c) => s + c.bytes, 0);
          sub.textContent = `${bucket} — ${items.length} item(s), ${fmt(total)}`;
          sec.appendChild(sub);
          sec.appendChild(renderList(items));
        }
        box.appendChild(sec); continue;
      }
      sec.appendChild(renderList(g.candidates));
      box.appendChild(sec);
    }
  }

  function renderList(cands) {
    const ul = document.createElement('ul');
    for (const c of cands) {
      const li = document.createElement('li'); if (c.blocked) li.className = 'blocked';
      const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = marked.has(c.path);
      cb.disabled = c.verdict === 'keep' || !!c.blocked; cb.onchange = () => toggleMark(c);
      li.appendChild(cb);
      const s = document.createElement('span'); s.className = 'size'; s.textContent = fmt(c.bytes); li.appendChild(s);
      const v = document.createElement('span'); v.className = 'v-' + c.verdict; v.textContent = { safe: '✓', review: '?', keep: '–' }[c.verdict]; li.appendChild(v);
      const p = document.createElement('a'); p.textContent = (c.display_path || display(c.path)) + (c.nested_in ? ' (nested)' : '') + (c.blocked ? ' (running)' : '');
      p.href = '#'; p.onclick = e => { e.preventDefault(); showNode({ name: c.path.split('/').pop(), path: c.path, bytes: c.bytes, files: c.files }, null, true); };
      li.appendChild(p);
      const t = c.tags || {};
      const facts = [];
      if (t.downloaded) facts.push(`downloaded ${t.downloaded}${t.source ? ' from ' + t.source : ''}`);
      if (t.last_used) facts.push(`last launched ${t.last_used}`);
      if (t.owner) facts.push(`no app named “${t.owner}”`);
      const n = document.createElement('span'); n.className = 'note';
      n.textContent = facts.length ? facts.join(' · ') + ' — ' + c.note : c.note + (c.reasons.length > 1 ? ' — ' + c.reasons.slice(1).join('; ') : '');
      li.appendChild(n);
      ul.appendChild(li);
    }
    return ul;
  }

  function renderDocker(g) {
    const wrap = document.createElement('div'); wrap.className = 'docker';
    const intro = document.createElement('p'); intro.className = 'note';
    intro.textContent = 'Docker objects have no Trash and cannot be restored, so this page only advises. Copy a command, or run everything with the confirmation flow:';
    wrap.appendChild(intro);
    const cmd = document.createElement('div'); cmd.className = 'cmd';
    cmd.appendChild(codeWithCopy('macsweep docker prune'));
    wrap.appendChild(cmd);
    const ul = document.createElement('ul');
    for (const c of g.candidates) {
      const t = c.tags || {};
      const li = document.createElement('li');
      const s = document.createElement('span'); s.className = 'size'; s.textContent = fmt(c.bytes); li.appendChild(s);
      const k = document.createElement('span'); k.className = 'kind'; k.textContent = t.kind || ''; li.appendChild(k);
      const nm = document.createElement('span'); nm.textContent = (c.display_path || c.path).replace(/^\w+ /, ''); li.appendChild(nm);
      const n = document.createElement('span'); n.className = 'note'; n.textContent = c.note; li.appendChild(n);
      if (t.command) li.appendChild(codeWithCopy(t.command));
      ul.appendChild(li);
    }
    wrap.appendChild(ul);
    return wrap;
  }

  function codeWithCopy(text) {
    const span = document.createElement('span'); span.className = 'code';
    const code = document.createElement('code'); code.textContent = text; span.appendChild(code);
    const b = document.createElement('button'); b.className = 'small'; b.textContent = 'copy';
    b.onclick = e => { e.stopPropagation(); navigator.clipboard && navigator.clipboard.writeText(text); b.textContent = 'copied'; setTimeout(() => b.textContent = 'copy', 1200); };
    span.appendChild(b);
    return span;
  }

  job('/api/scan', loadCandidates);
})();
