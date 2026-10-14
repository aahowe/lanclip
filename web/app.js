(() => {
  'use strict';

  const $ = (s) => document.querySelector(s);
  const input = $('#input'), sendBtn = $('#send'), picker = $('#picker');
  const feed = $('#feed'), empty = $('#empty'), listHead = $('#list-head'), count = $('#count');
  const uploads = $('#uploads'), presence = $('#presence'), foot = $('#foot');
  const dropzone = $('#dropzone'), lightbox = $('#lightbox'), toastEl = $('#toast');
  const clearBtn = $('#clear');

  // ---------- i18n ----------
  const MESSAGES = {
    zh: {
      tagline: '局域网剪贴板', connecting: '连接中…', reconnecting: '重新连接中…', connected: '已连接',
      online: (n) => `${n} 台设备在线`,
      placeholder: '输入或粘贴文字，也可以直接粘贴截图、拖入文件…',
      pickFiles: '选择文件', addFile: '添加文件', toSend: '发送', send: '发送',
      recent: '最近内容', clearAll: '清空全部', confirmClear: '确认清空？', cleared: '已清空', clearFailed: '清空失败',
      emptyTitle: '这里还空着', emptyBody: '在任意设备上打开这个页面，<br>发送的文字、图片和文件会同步出现在所有设备上。',
      dropTitle: '松开即可上传', dropSub: '支持任意类型、多个文件', close: '关闭', viewImage: '查看大图',
      copy: '复制', copied: '已复制到剪贴板', copyFailed: '复制失败，请长按文字手动复制',
      download: '下载', delete: '删除', deleteFailed: '删除失败', expand: '展开全部', collapse: '收起',
      tagText: '文本', tagImage: '图片', tagFile: '文件',
      chars: (n) => `${n} 字`, lines: (n) => `${n} 行`,
      justNow: '刚刚', minAgo: (n) => `${n} 分钟前`, today: '今天', yesterday: '昨天', date: (m, d) => `${m}月${d}日`,
      days: (n) => `${n} 天`, hours: (n) => `${n} 小时`, minutes: (n) => `${n} 分钟`,
      keepFor: (d) => `内容保留 ${d}`, maxItems: (n) => `最多 ${n} 条`, maxFile: (s) => `单个文件上限 ${s}`,
      textTooLong: '文本过长', sendFailed: '发送失败', preparing: '准备上传…',
      tooLarge: (s) => `超过单个文件上限 ${s}`, uploadFailed: (c) => `上传失败（${c}）`, networkError: '网络错误，上传失败',
      cancel: '取消', serverDown: '无法连接服务器', screenshot: '截图',
    },
    en: {
      tagline: 'LAN clipboard', connecting: 'Connecting…', reconnecting: 'Reconnecting…', connected: 'Connected',
      online: (n) => `${n} devices online`,
      placeholder: 'Type or paste text — or paste a screenshot, or drop files anywhere…',
      pickFiles: 'Choose files', addFile: 'Add files', toSend: 'to send', send: 'Send',
      recent: 'Recent', clearAll: 'Clear all', confirmClear: 'Clear everything?', cleared: 'Cleared', clearFailed: 'Could not clear',
      emptyTitle: 'Nothing here yet', emptyBody: 'Open this page on any device on your network.<br>Text, images and files you send show up everywhere instantly.',
      dropTitle: 'Drop to upload', dropSub: 'Any file type, as many as you like', close: 'Close', viewImage: 'View full size',
      copy: 'Copy', copied: 'Copied to clipboard', copyFailed: 'Copy failed — long-press the text to copy it',
      download: 'Download', delete: 'Delete', deleteFailed: 'Could not delete', expand: 'Show all', collapse: 'Show less',
      tagText: 'Text', tagImage: 'Image', tagFile: 'File',
      chars: (n) => `${n} chars`, lines: (n) => `${n} lines`,
      justNow: 'Just now', minAgo: (n) => `${n} min ago`, today: 'Today', yesterday: 'Yesterday',
      date: (m, d) => new Date(2000, m - 1, d).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
      days: (n) => `${n} day${n > 1 ? 's' : ''}`, hours: (n) => `${n} hour${n > 1 ? 's' : ''}`, minutes: (n) => `${n} min`,
      keepFor: (d) => `Items kept for ${d}`, maxItems: (n) => `up to ${n} items`, maxFile: (s) => `${s} per file`,
      textTooLong: 'Text is too long', sendFailed: 'Send failed', preparing: 'Preparing…',
      tooLarge: (s) => `Larger than the ${s} limit`, uploadFailed: (c) => `Upload failed (${c})`, networkError: 'Network error — upload failed',
      cancel: 'Cancel', serverDown: 'Cannot reach the server', screenshot: 'Screenshot',
    },
  };
  const lang = /^zh/i.test(navigator.language || '') ? 'zh' : 'en';
  const M = MESSAGES[lang];
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
  document.querySelectorAll('[data-i18n]').forEach((n) => { n.textContent = M[n.dataset.i18n]; });
  document.querySelectorAll('[data-i18n-html]').forEach((n) => { n.innerHTML = M[n.dataset.i18nHtml]; });
  document.querySelectorAll('[data-i18n-ph]').forEach((n) => { n.placeholder = M[n.dataset.i18nPh]; });
  document.querySelectorAll('[data-i18n-title]').forEach((n) => {
    n.title = M[n.dataset.i18nTitle];
    n.setAttribute('aria-label', M[n.dataset.i18nTitle]);
  });

  const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  if (!isMac) $('#mod').textContent = 'Ctrl';

  let config = { ttl: 0, maxFile: 0, textLimit: 1 << 20 };
  const items = new Map(); // id -> item

  // ---------- helpers ----------
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  };
  const icon = (name) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    s.setAttribute('class', 'ic');
    const u = document.createElementNS('http://www.w3.org/2000/svg', 'use');
    u.setAttribute('href', '#i-' + name);
    s.appendChild(u);
    return s;
  };
  const iconBtn = (name, title, cls = '') => {
    const b = el('button', 'icon-btn ' + cls);
    b.type = 'button';
    b.title = title;
    b.setAttribute('aria-label', title);
    b.appendChild(icon(name));
    return b;
  };

  function fmtSize(n) {
    if (n < 1024) return n + ' B';
    const u = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return +(n >= 100 ? n.toFixed(0) : n >= 10 ? n.toFixed(1) : n.toFixed(2)) + ' ' + u[i];
  }

  function fmtTime(iso) {
    const t = new Date(iso), s = (Date.now() - t) / 1000;
    if (s < 45) return M.justNow;
    if (s < 3600) return M.minAgo(Math.round(s / 60));
    const today = new Date(); today.setHours(0, 0, 0, 0);
    const hm = t.toLocaleTimeString(lang === 'zh' ? 'zh-CN' : 'en-GB', { hour: '2-digit', minute: '2-digit', hour12: false });
    if (t >= today) return M.today + ' ' + hm;
    if (t >= today - 864e5) return M.yesterday + ' ' + hm;
    return M.date(t.getMonth() + 1, t.getDate()) + ' ' + hm;
  }

  function fmtDuration(sec) {
    if (sec % 86400 === 0) return M.days(sec / 86400);
    if (sec % 3600 === 0) return M.hours(sec / 3600);
    return M.minutes(Math.round(sec / 60));
  }

  let toastTimer;
  function toast(msg, error = false) {
    toastEl.textContent = msg;
    toastEl.classList.toggle('error', error);
    toastEl.classList.add('on');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toastEl.classList.remove('on'), error ? 3200 : 1800);
  }

  // navigator.clipboard needs HTTPS; plain-HTTP LAN pages fall back to execCommand.
  async function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      try { await navigator.clipboard.writeText(text); return true; } catch {}
    }
    const ta = el('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none';
    document.body.appendChild(ta);
    ta.select();
    ta.setSelectionRange(0, text.length);
    let ok = false;
    try { ok = document.execCommand('copy'); } catch {}
    ta.remove();
    return ok;
  }

  const EXT_COLORS = {
    pdf: '#e5484d', doc: '#2f6fed', docx: '#2f6fed', xls: '#1f9d55', xlsx: '#1f9d55', csv: '#1f9d55',
    ppt: '#e8590c', pptx: '#e8590c', zip: '#a16207', rar: '#a16207', '7z': '#a16207', gz: '#a16207', tar: '#a16207',
    mp4: '#7c3aed', mov: '#7c3aed', mkv: '#7c3aed', mp3: '#db2777', wav: '#db2777', flac: '#db2777', m4a: '#db2777',
    apk: '#16a34a', dmg: '#475569', exe: '#475569', pkg: '#475569', iso: '#475569',
    txt: '#64748b', md: '#334155', json: '#0f766e', js: '#ca8a04', ts: '#2563eb', py: '#2563eb', go: '#0891b2',
    html: '#ea580c', css: '#2563eb', svg: '#d97706', psd: '#0369a1', ai: '#c2410c', sketch: '#d97706', fig: '#7c3aed',
  };
  const extOf = (name) => {
    const m = /\.([a-z0-9]{1,6})$/i.exec(name || '');
    return m ? m[1].toLowerCase() : '';
  };
  const fileURL = (it, dl) => 'api/files/' + it.id + (dl ? '?dl=1' : '');
  const isImage = (it) => it.kind === 'file' && /^image\/(png|jpe?g|gif|webp|avif|bmp)$/i.test(it.mime);

  // Linkify without innerHTML.
  const URL_RE = /\bhttps?:\/\/[^\s<>"'，。！？、）】]+/gi;
  function fillText(node, text) {
    let last = 0;
    for (const m of text.matchAll(URL_RE)) {
      if (m.index > last) node.appendChild(document.createTextNode(text.slice(last, m.index)));
      const a = el('a', null, m[0]);
      a.href = m[0]; a.target = '_blank'; a.rel = 'noopener noreferrer';
      node.appendChild(a);
      last = m.index + m[0].length;
    }
    if (last < text.length) node.appendChild(document.createTextNode(text.slice(last)));
  }

  // ---------- rendering ----------
  function metaRow(it, tag, extra) {
    const meta = el('div', 'meta');
    meta.appendChild(el('span', 'tag', tag));
    if (extra) { meta.appendChild(el('span', null, extra)); meta.appendChild(el('span', 'sep', '·')); }
    const t = el('span', null, fmtTime(it.created));
    t.dataset.time = it.created;
    meta.appendChild(t);
    return meta;
  }

  function deleteBtn(it) {
    const b = iconBtn('trash', M.delete, 'danger');
    b.onclick = async () => {
      b.disabled = true;
      const r = await fetch('api/items/' + it.id, { method: 'DELETE' }).catch(() => null);
      if (r && r.ok) removeItem(it.id);
      else { b.disabled = false; toast(M.deleteFailed, true); }
    };
    return b;
  }

  function downloadBtn(it) {
    const a = el('a', 'icon-btn primary');
    a.href = fileURL(it, true);
    a.title = M.download;
    a.setAttribute('aria-label', M.download);
    a.setAttribute('download', it.name);
    a.appendChild(icon('download'));
    return a;
  }

  function renderText(it) {
    const card = el('li', 'card text-card');
    const body = el('div', 'card-body');
    const pre = el('p', 'text');
    if (it.text.includes('\n') && /^#!|^\s*[{[]|^(\s{2,}|\t)\S|;\s*$|^\s*(import|const|let|func|def|class|#include|set -|export |sudo |docker |git )/m.test(it.text)) {
      pre.classList.add('code');
    }
    fillText(pre, it.text);
    body.appendChild(pre);

    const foot = el('div', 'card-foot');
    const lines = it.text.split('\n').length;
    foot.appendChild(metaRow(it, M.tagText, M.chars([...it.text].length) + (lines > 1 ? ' · ' + M.lines(lines) : '')));

    const copy = iconBtn('copy', M.copy, 'primary');
    copy.onclick = async () => {
      if (await copyText(it.text)) {
        copy.classList.add('done');
        copy.firstChild.firstChild.setAttribute('href', '#i-check');
        toast(M.copied);
        setTimeout(() => {
          copy.classList.remove('done');
          copy.firstChild.firstChild.setAttribute('href', '#i-copy');
        }, 1500);
      } else toast(M.copyFailed, true);
    };
    foot.append(copy, deleteBtn(it));
    card.append(body, foot);

    // Clamp long text with an expand toggle once laid out.
    requestAnimationFrame(() => {
      if (pre.scrollHeight > pre.clientHeight + 4) {
        pre.classList.add('clamped');
        const more = el('button', 'more');
        more.type = 'button';
        more.append(el('span', null, M.expand), icon('expand'));
        more.onclick = () => {
          const open = pre.classList.toggle('open');
          pre.classList.toggle('clamped', !open);
          more.classList.toggle('open', open);
          more.firstChild.textContent = open ? M.collapse : M.expand;
        };
        body.appendChild(more);
      }
    });
    return card;
  }

  function renderImage(it) {
    const card = el('li', 'card image');
    const body = el('div', 'card-body');
    const thumb = el('button', 'thumb');
    thumb.type = 'button';
    thumb.title = M.viewImage;
    const img = el('img');
    img.loading = 'lazy';
    img.decoding = 'async';
    img.alt = it.name;
    img.src = fileURL(it);
    thumb.appendChild(img);
    thumb.onclick = () => openLightbox(img.src, it.name);
    const name = el('div', 'name', it.name);
    body.append(thumb, name);

    const foot = el('div', 'card-foot');
    foot.appendChild(metaRow(it, M.tagImage, fmtSize(it.size)));
    foot.append(downloadBtn(it), deleteBtn(it));
    card.append(body, foot);
    return card;
  }

  function renderFile(it) {
    const card = el('li', 'card file');
    const body = el('div', 'card-body');
    const row = el('div', 'file-row');
    const ext = extOf(it.name);
    const badge = el('div', 'ext');
    badge.style.setProperty('--c', EXT_COLORS[ext] || '#64748b');
    if (ext) badge.appendChild(el('span', null, ext.toUpperCase().slice(0, 4)));
    else badge.appendChild(icon('file'));
    const info = el('div');
    info.style.minWidth = '0';
    info.append(el('div', 'name', it.name), el('div', 'sub', fmtSize(it.size)));
    row.append(badge, info);
    body.appendChild(row);

    const foot = el('div', 'card-foot');
    foot.appendChild(metaRow(it, M.tagFile));
    foot.append(downloadBtn(it), deleteBtn(it));
    card.append(body, foot);
    return card;
  }

  function renderItem(it) {
    const card = it.kind === 'text' ? renderText(it) : isImage(it) ? renderImage(it) : renderFile(it);
    card.dataset.id = it.id;
    return card;
  }

  function refreshChrome() {
    const n = items.size;
    empty.hidden = n > 0;
    listHead.hidden = n === 0;
    count.textContent = n;
  }

  function addItem(it, animate = true) {
    if (items.has(it.id)) return;
    items.set(it.id, it);
    const card = renderItem(it);
    if (animate) card.classList.add('fresh');
    feed.prepend(card);
    refreshChrome();
  }

  function removeItem(id) {
    if (!items.delete(id)) return;
    const card = feed.querySelector(`[data-id="${CSS.escape(id)}"]`);
    if (card) {
      card.classList.add('leaving');
      card.addEventListener('animationend', () => card.remove(), { once: true });
      setTimeout(() => card.remove(), 400);
    }
    refreshChrome();
  }

  async function sync() {
    const r = await fetch('api/items', { cache: 'no-store' });
    if (!r.ok) throw new Error('load failed');
    const data = await r.json();
    config = data.config;
    const ids = new Set(data.items.map((i) => i.id));
    for (const id of [...items.keys()]) if (!ids.has(id)) removeItem(id);
    // Server sends newest first; prepend oldest first.
    for (const it of [...data.items].reverse()) addItem(it, false);
    const parts = [];
    if (config.ttl > 0) parts.push(M.keepFor(fmtDuration(config.ttl)));
    parts.push(M.maxItems(config.maxItems));
    parts.push(M.maxFile(fmtSize(config.maxFile)));
    foot.textContent = parts.join(' · ');
    refreshChrome();
  }

  // ---------- realtime ----------
  function setPresence(state, clients) {
    presence.dataset.state = state;
    const t = presence.querySelector('.presence-text');
    if (state === 'online') t.textContent = clients > 1 ? M.online(clients) : M.connected;
    else if (state === 'offline') t.textContent = M.reconnecting;
    else t.textContent = M.connecting;
  }

  function connect() {
    const es = new EventSource('api/events');
    es.onopen = () => {
      setPresence('online', 1);
      sync().catch(() => {});
    };
    es.onerror = () => setPresence('offline');
    es.onmessage = (e) => {
      const ev = JSON.parse(e.data);
      if (ev.type === 'add') addItem(ev.item);
      else if (ev.type === 'remove') ev.ids.forEach(removeItem);
      else if (ev.type === 'presence') setPresence('online', ev.clients);
    };
  }

  // ---------- send text ----------
  function autosize() {
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, window.innerHeight * 0.5) + 'px';
    sendBtn.disabled = !input.value.trim();
  }
  input.addEventListener('input', autosize);

  async function sendText() {
    const text = input.value;
    if (!text.trim()) return;
    if (text.length > config.textLimit) return toast(M.textTooLong, true);
    sendBtn.disabled = true;
    try {
      const r = await fetch('api/text', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      });
      const data = await r.json();
      if (!r.ok) throw new Error(r.status === 413 ? M.textTooLong : M.sendFailed);
      addItem(data);
      input.value = '';
      autosize();
    } catch (e) {
      toast(e.message || M.sendFailed, true);
      sendBtn.disabled = false;
    }
  }
  sendBtn.onclick = sendText;
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing) {
      e.preventDefault();
      sendText();
    }
  });

  // ---------- upload files ----------
  const pad = (n) => String(n).padStart(2, '0');
  function nameFor(file) {
    // Pasted screenshots arrive as "image.png"; give them a useful name.
    if (file.name && !/^image\.(png|jpe?g|gif|webp)$/i.test(file.name)) return file.name;
    const d = new Date();
    const ext = (file.type.split('/')[1] || 'png').replace('jpeg', 'jpg');
    return `${M.screenshot} ${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}.${pad(d.getMinutes())}.${pad(d.getSeconds())}.${ext}`;
  }

  function upload(file) {
    const name = nameFor(file);
    const row = el('div', 'upload');
    const info = el('div', 'upload-body');
    const bar = el('div', 'bar');
    const fill = el('i');
    bar.appendChild(fill);
    const meta = el('div', 'upload-meta', M.preparing);
    info.append(el('div', 'upload-name', name), bar, meta);
    const cancel = iconBtn('x', M.cancel);
    row.append(icon('upload'), info, cancel);
    uploads.appendChild(row);

    const done = (delay = 0) => setTimeout(() => row.remove(), delay);
    const fail = (msg) => {
      row.classList.add('failed');
      meta.textContent = msg;
      cancel.onclick = () => done();
      done(6000);
    };

    if (config.maxFile && file.size > config.maxFile) return fail(M.tooLarge(fmtSize(config.maxFile)));

    const xhr = new XMLHttpRequest();
    xhr.open('POST', 'api/files?name=' + encodeURIComponent(name));
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    const start = performance.now();
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      const pct = e.loaded / e.total;
      fill.style.width = (pct * 100).toFixed(1) + '%';
      const secs = (performance.now() - start) / 1000;
      const speed = secs > 0.3 ? ` · ${fmtSize(e.loaded / secs)}/s` : '';
      meta.textContent = `${fmtSize(e.loaded)} / ${fmtSize(e.total)}${speed}`;
    };
    xhr.onload = () => {
      if (xhr.status === 201) {
        let data = {};
        try { data = JSON.parse(xhr.responseText); } catch {}
        addItem(data);
        fill.style.width = '100%';
        done();
      } else fail(xhr.status === 413 ? M.tooLarge(fmtSize(config.maxFile)) : M.uploadFailed(xhr.status));
    };
    xhr.onerror = () => fail(M.networkError);
    xhr.onabort = () => done();
    cancel.onclick = () => xhr.abort();
    xhr.send(file);
  }

  const uploadAll = (files) => [...files].forEach(upload);

  picker.onchange = () => { uploadAll(picker.files); picker.value = ''; };

  document.addEventListener('paste', (e) => {
    const files = [...(e.clipboardData?.files || [])];
    if (files.length) {
      e.preventDefault();
      uploadAll(files);
    }
  });

  let dragDepth = 0;
  const hasFiles = (e) => [...(e.dataTransfer?.types || [])].includes('Files');
  window.addEventListener('dragenter', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    if (++dragDepth === 1) dropzone.classList.add('on');
  });
  window.addEventListener('dragover', (e) => { if (hasFiles(e)) e.preventDefault(); });
  window.addEventListener('dragleave', (e) => {
    if (!hasFiles(e)) return;
    if (--dragDepth <= 0) { dragDepth = 0; dropzone.classList.remove('on'); }
  });
  window.addEventListener('drop', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    dropzone.classList.remove('on');
    uploadAll(e.dataTransfer.files);
  });

  // ---------- clear all (two-step) ----------
  let armTimer;
  clearBtn.onclick = async () => {
    if (!clearBtn.classList.contains('armed')) {
      clearBtn.classList.add('armed');
      clearBtn.textContent = M.confirmClear;
      armTimer = setTimeout(() => { clearBtn.classList.remove('armed'); clearBtn.textContent = M.clearAll; }, 3000);
      return;
    }
    clearTimeout(armTimer);
    clearBtn.classList.remove('armed');
    clearBtn.textContent = M.clearAll;
    const r = await fetch('api/items', { method: 'DELETE' }).catch(() => null);
    if (r && r.ok) { [...items.keys()].forEach(removeItem); toast(M.cleared); }
    else toast(M.clearFailed, true);
  };

  // ---------- lightbox ----------
  function openLightbox(src, alt) {
    const img = lightbox.querySelector('img');
    img.src = src;
    img.alt = alt;
    lightbox.hidden = false;
  }
  lightbox.onclick = () => { lightbox.hidden = true; };
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') lightbox.hidden = true; });

  // ---------- go ----------
  setInterval(() => {
    document.querySelectorAll('[data-time]').forEach((n) => { n.textContent = fmtTime(n.dataset.time); });
  }, 30000);

  sync().catch(() => toast(M.serverDown, true)).finally(refreshChrome);
  connect();
})();
