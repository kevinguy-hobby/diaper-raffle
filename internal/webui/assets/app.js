/*
 * The raffle page.
 *
 * The browser deliberately does not know how to pick a winner, and does not
 * hold one it has not been shown: the draw runs on the server, and an untorn
 * stub arrives with no name on it. Tapping a stub is a request, not a reveal
 * of something already sitting in memory.
 */
(function () {
  'use strict';

  var SAVE_DEBOUNCE_MS = 600;
  var DEFAULT_EVENT_NAME = "Jen's Baby Shower";

  var el = {
    toast: document.getElementById('toast'),
    eyebrow: document.getElementById('eyebrow'),
    picker: document.getElementById('eventpick'),
    renameEvent: document.getElementById('renameevent'),
    newEvent: document.getElementById('newevent'),
    signOut: document.getElementById('signout'),
    roster: document.getElementById('roster'),
    saveFlag: document.getElementById('saveflag'),
    tally: document.getElementById('tally'),
    draw: document.getElementById('draw'),
    substage: document.getElementById('substage'),
    stubs: document.getElementById('stubs'),
    oddsPanel: document.getElementById('oddspanel'),
    oddsBody: document.getElementById('oddsbody'),
    oddsNote: document.getElementById('oddsnote'),
    historyPanel: document.getElementById('historypanel'),
    historyBody: document.getElementById('historybody')
  };

  var state = {
    slug: null,
    event: null,
    guests: [],
    tally: null,
    draw: null,
    stale: false,
    drawing: false,
    revealing: {}
  };

  /* ---------- talking to the server ---------- */

  function api(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (res) {
      if (res.status === 204) return null;
      return res.text().then(function (text) {
        var data = null;
        if (text) {
          try { data = JSON.parse(text); } catch (e) { /* handled below */ }
        }
        if (res.status === 401) {
          // The session expired or the password changed. Reloading gets the
          // sign-in page from the server rather than us faking one here.
          window.location.reload();
          throw new Error('Signed out.');
        }
        if (!res.ok) {
          var message = data && data.error && data.error.message;
          throw new Error(message || ('The server said no (' + res.status + ').'));
        }
        if (data === null) throw new Error('The server sent something unreadable.');
        return data;
      });
    });
  }

  var toastTimer = null;
  function complain(err) {
    var message = (err && err.message) || String(err);
    el.toast.textContent = message;
    el.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.toast.hidden = true; }, 6000);
  }

  /* ---------- small helpers ---------- */

  function esc(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  function num(n) { return Number(n || 0).toLocaleString(); }

  function plural(n, one, many) { return n === 1 ? one : many; }

  function slugFromLocation() {
    var m = window.location.pathname.match(/^\/e\/([^/]+)\/?$/);
    return m ? decodeURIComponent(m[1]) : null;
  }

  function spell(n) {
    var words = ['zero', 'one', 'two', 'three', 'four', 'five', 'six',
      'seven', 'eight', 'nine', 'ten'];
    return words[n] || String(n);
  }

  /* ---------- state ---------- */

  // applyView takes a whole-page payload from the server. The roster text is
  // left alone unless asked for, because the host may be mid-sentence.
  function applyView(view, adoptRosterText) {
    state.event = view.event;
    state.guests = view.guests || [];
    state.tally = view.tally;
    state.draw = view.draw;
    state.stale = !!view.stale;

    if (adoptRosterText) el.roster.value = view.event.roster_text;

    render();
  }

  function render() {
    renderMasthead();
    renderTally();
    renderStubs();
    renderSubstage();
  }

  function renderMasthead() {
    document.title = state.event.name + ' · Diaper Raffle';
    el.eyebrow.textContent = state.event.name + ' · one diaper, one ticket';
  }

  function renderTally() {
    var t = state.tally;
    var prizes = state.event.prize_count;

    var bits = [
      '<div><span>In the draw</span><b>' + num(t.entrants) + '</b></div>',
      '<div><span>Diapers</span><b>' + num(t.diaper_total) + '</b></div>'
    ];
    if (t.sitting_out) {
      bits.push('<div><span>Sitting out (zero)</span><b>' + num(t.sitting_out) + '</b></div>');
    }
    if (t.merged_names) {
      bits.push('<div><span>Names merged</span><b>' + num(t.merged_names) + '</b></div>');
    }
    if (t.short) {
      bits.push('<div><span class="warn">Heads up</span><b class="warn">Only ' +
        num(t.entrants) + ' can win</b></div>');
    }
    bits.push(
      '<div><span>Prizes</span><div class="stepper">' +
      '<button type="button" data-step="-1" aria-label="One fewer prize"' +
      (prizes <= 1 ? ' disabled' : '') + '>−</button>' +
      '<output id="prizecount">' + prizes + '</output>' +
      '<button type="button" data-step="1" aria-label="One more prize"' +
      (prizes >= 20 ? ' disabled' : '') + '>+</button>' +
      '</div></div>'
    );

    el.tally.innerHTML = bits.join('');
    el.draw.disabled = t.entrants === 0 || state.drawing;
  }

  function renderStubs() {
    var prizes = state.event.prize_count;
    el.stubs.classList.toggle('many', prizes > 3);

    if (!state.draw) {
      var blanks = [];
      for (var i = 1; i <= prizes; i++) {
        blanks.push(
          '<div class="stub empty"><div class="stub-head">' +
          '<span class="prize" style="color:inherit">Prize ' + i + '</span></div>' +
          '<div class="stub-body"><div class="tapme" style="color:inherit">Not drawn yet</div></div></div>'
        );
      }
      el.stubs.innerHTML = blanks.join('');
      return;
    }

    el.stubs.innerHTML = state.draw.winners.map(function (w) {
      var pending = !!state.revealing[w.prize_index];
      var cls = w.revealed ? 'open' : (pending ? 'ready pending' : 'ready');
      var body = w.revealed
        ? '<span class="name">' + esc(w.name) + '</span>' +
          '<span class="count">' + num(w.diaper_count) + ' ' +
          plural(w.diaper_count, 'diaper', 'diapers') + ' in the bowl</span>'
        : '<span class="facedown">? ? ?</span>' +
          '<span class="count tapme">' + (pending ? 'Tearing…' : 'Tap to tear open') + '</span>';

      return '<button class="stub ' + cls + '" type="button" data-i="' + w.prize_index + '"' +
        (w.revealed ? ' aria-disabled="true"' : '') + '>' +
        '<span class="perf"></span>' +
        '<span class="stub-head"><span class="prize">Prize ' + (w.prize_index + 1) + '</span>' +
        '<span class="serial">No. ' + esc(w.serial) + '</span></span>' +
        '<span class="stub-body">' + body + '</span>' +
        '</button>';
    }).join('');
  }

  function renderSubstage() {
    var prizes = state.event.prize_count;
    el.substage.classList.remove('warn');

    if (state.drawing) {
      el.substage.textContent = 'Drawing…';
      return;
    }
    if (!state.draw) {
      el.draw.textContent = 'Draw winners';
      if (state.tally.entrants === 0) {
        el.substage.textContent = 'Add at least one guest with diapers';
      } else {
        el.substage.textContent = spell(prizes) + ' ' + plural(prizes, 'prize', 'prizes') +
          ' · ' + spell(prizes) + ' different ' + plural(prizes, 'person', 'people');
      }
      return;
    }

    el.draw.textContent = 'Draw again';

    if (state.stale) {
      el.substage.classList.add('warn');
      el.substage.textContent = 'Roster changed since this draw · draw again to use it';
      return;
    }

    var winners = state.draw.winners;
    var revealed = winners.filter(function (w) { return w.revealed; }).length;

    if (revealed === winners.length) {
      el.substage.textContent = 'All ' + spell(winners.length) + ' revealed';
    } else if (winners.length < prizes) {
      el.substage.textContent = 'Only ' + spell(winners.length) + ' eligible · tap to reveal';
    } else {
      el.substage.textContent = 'Drawn · tap each stub to reveal';
    }
  }

  /* ---------- roster autosave ---------- */

  var saveTimer = null;
  var saveSeq = 0;
  var savedSeq = 0;
  var pendingText = null;

  function markSaving() {
    el.saveFlag.className = 'saveflag saving';
    el.saveFlag.textContent = 'Saving…';
  }

  function markSaved() {
    var when = new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    el.saveFlag.className = 'saveflag';
    el.saveFlag.textContent = 'Saved ' + when;
  }

  function markFailed() {
    el.saveFlag.className = 'saveflag failed';
    el.saveFlag.textContent = 'Not saved';
  }

  function scheduleSave() {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(saveRoster, SAVE_DEBOUNCE_MS);
  }

  function saveRoster() {
    clearTimeout(saveTimer);
    var text = el.roster.value;
    if (text === pendingText) return Promise.resolve();

    pendingText = text;
    var seq = ++saveSeq;
    markSaving();

    return api('PUT', '/api/events/' + encodeURIComponent(state.slug) + '/roster', { text: text })
      .then(function (view) {
        // A slower earlier save must not overwrite a newer one's result.
        if (seq < savedSeq) return;
        savedSeq = seq;
        applyView(view, false);
        markSaved();
        if (el.oddsPanel.open) loadOdds();
      })
      .catch(function (err) {
        if (seq < savedSeq) return;
        pendingText = null;
        markFailed();
        complain(err);
      });
  }

  el.roster.addEventListener('input', function () {
    el.saveFlag.className = 'saveflag';
    el.saveFlag.textContent = 'Unsaved';
    scheduleSave();
  });

  el.roster.addEventListener('blur', function () {
    if (el.roster.value !== pendingText) saveRoster();
  });

  // A roster typed and then immediately closed should still land.
  window.addEventListener('pagehide', function () {
    if (!state.slug || el.roster.value === pendingText) return;
    var payload = JSON.stringify({ text: el.roster.value });
    var url = '/api/events/' + encodeURIComponent(state.slug) + '/roster';
    // sendBeacon cannot do PUT, so use a keepalive fetch.
    fetch(url, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: payload,
      keepalive: true
    }).catch(function () { /* the tab is going away regardless */ });
  });

  /* ---------- drawing and revealing ---------- */

  el.draw.addEventListener('click', function () {
    if (state.drawing) return;

    // Flush anything typed in the last few hundred milliseconds, so the draw
    // runs against what is on screen rather than what was last saved.
    var ready = el.roster.value !== pendingText ? saveRoster() : Promise.resolve();

    state.drawing = true;
    el.draw.disabled = true;
    renderSubstage();

    ready
      .then(function () {
        return api('POST', '/api/events/' + encodeURIComponent(state.slug) + '/draws');
      })
      .then(function (data) {
        state.drawing = false;
        state.draw = data.draw;
        state.stale = false;
        state.revealing = {};
        render();
        riffle();
        if (el.historyPanel.open) loadHistory();
      })
      .catch(function (err) {
        state.drawing = false;
        render();
        complain(err);
      });
  });

  function riffle() {
    var nodes = el.stubs.querySelectorAll('.stub');
    Array.prototype.forEach.call(nodes, function (node, i) {
      node.classList.remove('riffling');
      void node.offsetWidth;
      node.style.animationDelay = (i * 0.06) + 's';
      node.classList.add('riffling');
    });
  }

  el.stubs.addEventListener('click', function (ev) {
    var btn = ev.target.closest('.stub');
    if (!btn || btn.classList.contains('empty') || !state.draw) return;

    var index = parseInt(btn.dataset.i, 10);
    if (isNaN(index) || state.revealing[index]) return;

    var winner = state.draw.winners.find(function (w) { return w.prize_index === index; });
    if (!winner || winner.revealed) return;

    state.revealing[index] = true;
    renderStubs();

    api('POST', '/api/draws/' + state.draw.id + '/winners/' + index + '/reveal')
      .then(function (data) {
        delete state.revealing[index];
        state.draw.winners = state.draw.winners.map(function (w) {
          return w.prize_index === index ? data.winner : w;
        });
        renderStubs();
        renderSubstage();

        var node = el.stubs.querySelector('[data-i="' + index + '"]');
        if (node) node.classList.add('tearing');
        if (el.historyPanel.open) loadHistory();
      })
      .catch(function (err) {
        delete state.revealing[index];
        renderStubs();
        complain(err);
      });
  });

  /* ---------- prize count ---------- */

  el.tally.addEventListener('click', function (ev) {
    var btn = ev.target.closest('[data-step]');
    if (!btn || btn.disabled) return;

    var next = state.event.prize_count + parseInt(btn.dataset.step, 10);
    if (next < 1 || next > 20) return;

    api('PATCH', '/api/events/' + encodeURIComponent(state.slug), { prize_count: next })
      .then(function (view) {
        applyView(view, false);
        if (el.oddsPanel.open) loadOdds();
      })
      .catch(complain);
  });

  /* ---------- odds ---------- */

  function loadOdds() {
    return api('GET', '/api/events/' + encodeURIComponent(state.slug) + '/odds')
      .then(function (data) { renderOdds(data.odds, data.runs); })
      .catch(function (err) {
        el.oddsBody.innerHTML = '<tr><td colspan="4">Could not work out the odds.</td></tr>';
        complain(err);
      });
  }

  function renderOdds(rows, runs) {
    var prizes = state.event.prize_count;
    el.oddsNote.textContent = 'Chance of finishing in the top ' + spell(prizes) +
      ' across ' + num(runs) + ' simulated draws, so it already accounts for one win per person.';

    if (!rows.length) {
      el.oddsBody.innerHTML = '<tr><td colspan="4">Nobody is on the roster yet.</td></tr>';
      return;
    }

    var max = rows.reduce(function (m, r) { return Math.max(m, r.chance); }, 0) || 1;

    el.oddsBody.innerHTML = rows.map(function (r) {
      var width = Math.max(1, Math.round(r.chance / max * 100));
      return '<tr class="' + (r.eligible ? '' : 'out') + '">' +
        '<td>' + esc(r.name) + (r.merged ? ' <span class="tag">merged</span>' : '') + '</td>' +
        '<td class="n">' + num(r.diaper_count) + '</td>' +
        '<td class="n">' + (r.eligible ? Math.round(r.chance * 100) + '%' : '—') + '</td>' +
        '<td>' + (r.eligible ? '<i class="bar" style="width:' + width + '%"></i>' : '') + '</td>' +
        '</tr>';
    }).join('');
  }

  el.oddsPanel.addEventListener('toggle', function () {
    if (el.oddsPanel.open) loadOdds();
  });

  /* ---------- history ---------- */

  function loadHistory() {
    return api('GET', '/api/events/' + encodeURIComponent(state.slug) + '/draws')
      .then(function (data) { renderHistory(data.draws); })
      .catch(function (err) {
        el.historyBody.innerHTML = '<p class="hint">Could not load the past draws.</p>';
        complain(err);
      });
  }

  function renderHistory(draws) {
    if (!draws.length) {
      el.historyBody.innerHTML = '<p class="hint">No draws yet. Press the button.</p>';
      return;
    }

    el.historyBody.innerHTML = draws.map(function (d) {
      var when = new Date(d.created_at).toLocaleString([], {
        month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit'
      });
      var isCurrent = state.draw && d.id === state.draw.id;

      var winners = d.winners.map(function (w) {
        var label = w.revealed
          ? esc(w.name) + ' <span class="sealed">' + num(w.diaper_count) + '</span>'
          : '<span class="sealed">still sealed</span>';
        return '<li><span class="place">Prize ' + (w.prize_index + 1) + '</span>' + label + '</li>';
      }).join('');

      return '<div class="draw-record">' +
        '<div class="draw-when">' +
        '<span' + (isCurrent ? ' class="current"' : '') + '>' + esc(when) +
        (isCurrent ? ' · on the table' : '') + '</span>' +
        '<span>' + num(d.entrant_count) + ' ' + plural(d.entrant_count, 'entrant', 'entrants') + '</span>' +
        '<span>' + num(d.diaper_total) + ' diapers</span>' +
        '</div>' +
        '<ul class="draw-list">' + winners + '</ul>' +
        '</div>';
    }).join('');
  }

  el.historyPanel.addEventListener('toggle', function () {
    if (el.historyPanel.open) loadHistory();
  });

  /* ---------- switching and creating raffles ---------- */

  function renderPicker(events) {
    el.picker.innerHTML = events.map(function (e) {
      return '<option value="' + esc(e.slug) + '"' +
        (e.slug === state.slug ? ' selected' : '') + '>' + esc(e.name) + '</option>';
    }).join('');
  }

  el.picker.addEventListener('change', function () {
    window.location.href = '/e/' + encodeURIComponent(el.picker.value);
  });

  // Renaming moves the link to match, because a raffle called one thing living
  // at a URL that says another is just confusing. The server only moves the
  // slug when it is explicitly told to, so this is the one place it happens.
  el.renameEvent.addEventListener('click', function () {
    var name = window.prompt('Rename this raffle', state.event.name);
    if (name === null) return;
    name = name.trim();
    if (!name || name === state.event.name) return;

    api('PATCH', '/api/events/' + encodeURIComponent(state.slug),
      { name: name, slug: name })
      .then(function (view) {
        var moved = view.event.slug !== state.slug;
        state.slug = view.event.slug;
        applyView(view, false);

        if (moved) {
          window.history.replaceState({}, '', '/e/' + encodeURIComponent(state.slug));
        }
        return api('GET', '/api/events').then(function (data) {
          renderPicker(data.events);
        });
      })
      .catch(complain);
  });

  el.newEvent.addEventListener('click', function () {
    var name = window.prompt('Name this raffle', DEFAULT_EVENT_NAME);
    if (name === null) return;
    name = name.trim();
    if (!name) return;

    api('POST', '/api/events', { name: name })
      .then(function (view) {
        window.location.href = '/e/' + encodeURIComponent(view.event.slug);
      })
      .catch(complain);
  });

  /* ---------- boot ---------- */

  // Only show a way out if there is something to be signed out of.
  function showSignOutIfLocked() {
    return api('GET', '/api/session')
      .then(function (session) {
        if (session.locked) el.signOut.hidden = false;
      })
      .catch(function () { /* not worth bothering anyone about */ });
  }

  el.signOut.addEventListener('click', function () {
    api('DELETE', '/api/session')
      .then(function () { window.location.reload(); })
      .catch(complain);
  });

  function boot() {
    var wanted = slugFromLocation();

    showSignOutIfLocked();

    api('GET', '/api/events')
      .then(function (data) {
        var events = data.events;

        if (wanted) return { slug: wanted, events: events };
        if (events.length) return { slug: events[0].slug, events: events };

        // First run: there is nothing to show, so make something to show.
        return api('POST', '/api/events', { name: DEFAULT_EVENT_NAME })
          .then(function (view) {
            return { slug: view.event.slug, events: [view.event] };
          });
      })
      .then(function (picked) {
        state.slug = picked.slug;
        window.history.replaceState({}, '', '/e/' + encodeURIComponent(picked.slug));

        return api('GET', '/api/events/' + encodeURIComponent(picked.slug))
          .then(function (view) {
            applyView(view, true);
            pendingText = el.roster.value;
            el.roster.disabled = false;

            var known = picked.events.some(function (e) { return e.slug === picked.slug; });
            renderPicker(known ? picked.events : [view.event].concat(picked.events));
          });
      })
      .catch(function (err) {
        el.substage.classList.add('warn');
        el.substage.textContent = 'Could not reach the server';
        complain(err);
      });
  }

  boot();
})();
