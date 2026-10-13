// The admin page's full-flow driver. TEST-ONLY: the harness proxy (main.go,
// started by run.sh) puts it at the top of the admin page's <head>, ahead
// of the page's own script. The real server never serves it.
//
// It stubs fetch and window.open for plex.tv ONLY: every request to the
// page's own origin passes through unchanged to the real server (through
// the proxy). Then it drives the page the way an admin would, pauses at
// checkpoints while run.sh checks the server with curl, and reports what
// it saw to /__flow/report.
(function () {
  "use strict";
  var CFG = window.__FLOW__;
  var K = CFG.keys;
  // This script carries the run's test secrets (CFG). Out of the page once
  // it has run, so the checks that look for secrets in the page see only
  // what the page itself put there.
  if (document.currentScript) document.currentScript.remove();
  try { delete window.__FLOW__; } catch (e) { window.__FLOW__ = undefined; }
  // NO_STORAGE=1 run.sh: storage throws, as in a private window with site
  // data blocked. The page must still sign in to Plex, on a per-page id.
  if (CFG.noStorage) {
    ["localStorage", "sessionStorage"].forEach(function (k) {
      Object.defineProperty(window, k, { configurable: true, get: function () {
        throw new DOMException("The operation is insecure.", "SecurityError");
      } });
    });
  }
  var realFetch = window.fetch.bind(window);
  var realNow = Date.now;
  var nowOffset = 0;
  var HOSTILE = "<img src=x onerror=alert(1)>";
  var SERVER_ID = "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b";
  var HOSTILE_ID = "aaaa1111bbbb2222cccc3333dddd4444eeee5555";
  var CLIENT_KEY = "alexandria-admin-plex-client-id";
  var seq = 0, inClick = false, step = "start";
  var checks = [];
  var rec = { plex: [], same: [], other: [], opens: [], navs: [], closes: 0,
              alerts: [], consoleCalls: 0, consoleHits: [], errors: [], csp: [] };
  var stub = { pin: 7000, pollsBeforeToken: 1, polls: {}, resources: null,
               sharedStatus: 200, homeStatus: 200, blockPopup: false, popup: null };

  // ---- plex.tv, as fixtures -----------------------------------------
  function conn(uri, local, relay) {
    return { protocol: uri.split(":")[0], address: "203.0.113.7", port: 32400, uri: uri,
             local: local, relay: relay, IPv6: false };
  }
  var STOUT_CONNS = [
    conn("https://192-168-1-9.abc123.plex.direct:32400", true, false),
    conn("https://203-0-113-7.abc123.plex.direct:32400", false, false),
    conn("https://2001-db8--9.abc123.plex.direct:32400", false, false),
    conn("https://198-51-100-20.def456.plex.direct:8443", false, true),
    conn("http://192.168.1.9:32400", true, false)
  ];
  var RESOURCES = [
    { name: "StoutOffice", product: "Plex Media Server", productVersion: "1.41.3.9314-a0bfb8370",
      platform: "Linux", device: "PC", clientIdentifier: SERVER_ID, provides: "server",
      owned: true, home: false, accessToken: "OWNER-SERVER-TOKEN-never-kept",
      publicAddress: "203.0.113.7", presence: true, connections: STOUT_CONNS },
    { name: HOSTILE, product: "Plex Media Server", provides: "server", owned: true,
      clientIdentifier: HOSTILE_ID,
      connections: [conn("https://10-0-0-2.zzz999.plex.direct:32400", true, false)] },
    { name: "Dad's Server", product: "Plex Media Server", provides: "server", owned: false,
      clientIdentifier: "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f", sourceTitle: "dad",
      connections: [conn("https://10-0-0-5.fff000.plex.direct:32400", false, false)] },
    { name: "Living Room", product: "Plex for Apple TV", provides: "client,player,pubsub-player",
      owned: true, clientIdentifier: "tv-0001", connections: [] }
  ];
  var NO_OWNED = [RESOURCES[2], RESOURCES[3]];
  var SHARED_STOUT =
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    '<MediaContainer friendlyName="myPlex" identifier="com.plexapp.plugins.myplex" ' +
      'machineIdentifier="' + SERVER_ID + '" size="5">\n' +
    '<SharedServer id="9001" username="" email="" userID="42" accessToken="' + K.grandma + '" ' +
      'name="StoutOffice" acceptedAt="1696000000" invitedAt="1695990000" allowSync="1" owned="0" ' +
      'allLibraries="1"><Section id="1" key="1" title="Movies" type="movie" shared="1"/></SharedServer>\n' +
    '<SharedServer acceptedAt="1697000000" accessToken="' + K.bob + '" userID="1002" ' +
      'email="bob@example.com" username="bobsmith" id="9002" name="StoutOffice"/>\n' +
    '<SharedServer id="9003" userID="1003" username="carol" email="carol@example.com" ' +
      'accessToken="' + K.carol + '" acceptedAt="" invitedAt="1697100000" name="StoutOffice"/>\n' +
    '<SharedServer id="9004" userID="1004" username="dave" email="dave@example.com" ' +
      'acceptedAt="1697200000" name="StoutOffice"/>\n' +
    '<SharedServer name="StoutOffice" acceptedAt="1697300000" accessToken="' + K.hostile + '" ' +
      'email="" username="" userID="43" id="9005"/>\n' +
    '</MediaContainer>\n';
  var SHARED_NOBODY =
    '<?xml version="1.0" encoding="UTF-8"?>\n<MediaContainer friendlyName="myPlex" size="0">\n' +
    '</MediaContainer>\n';
  var HOME_XML =
    '<?xml version="1.0" encoding="UTF-8"?>\n' +
    '<MediaContainer friendlyName="myPlex" identifier="com.plexapp.plugins.myplex" size="3">\n' +
    '<User id="1" uuid="a1b2" admin="1" guest="0" restricted="0" title="Chris" username="chris" ' +
      'email="chris@example.com"/>\n' +
    '<User title="Grandma" restricted="1" admin="0" id="42" uuid="b2c3" guest="0" username="" email=""/>\n' +
    '<User id="43" uuid="c3d4" admin="0" restricted="1" title="&lt;img src=x onerror=alert(1)&gt;" ' +
      'username="" email=""/>\n' +
    '</MediaContainer>\n';

  function answer(status, body, type) {
    return Promise.resolve(new Response(body, { status: status, headers: { "Content-Type": type } }));
  }
  function json(status, v) { return answer(status, JSON.stringify(v), "application/json"); }
  function xml(status, s) { return answer(status, s, "application/xml"); }

  function lowerHeaders(h) {
    var out = {};
    if (h && typeof h.forEach === "function" && !(h instanceof Array)) {
      h.forEach(function (v, k) { out[String(k).toLowerCase()] = String(v); });
    } else if (h) {
      Object.keys(h).forEach(function (k) { out[k.toLowerCase()] = String(h[k]); });
    }
    return out;
  }

  function plexAnswer(u, init) {
    var method = String(init.method || "GET").toUpperCase(), p = u.pathname;
    rec.plex.push({ seq: ++seq, t: performance.now(), method: method, path: p, search: u.search,
                    headers: lowerHeaders(init.headers), credentials: init.credentials || "",
                    inClick: inClick, step: step });
    if (init.signal && init.signal.aborted) {
      return Promise.reject(new DOMException("The operation was aborted.", "AbortError"));
    }
    var m;
    if (method === "POST" && p === "/api/v2/pins") {
      var id = ++stub.pin;
      stub.polls[id] = 0;
      return json(201, { id: id, code: "CODE" + id + "x9Q2", product: "Alexandria Companion",
                         trusted: false, expiresIn: 1800, authToken: null, newRegistration: null });
    }
    if (method === "GET" && (m = p.match(/^\/api\/v2\/pins\/(\d+)$/))) {
      var pid = Number(m[1]);
      stub.polls[pid] = (stub.polls[pid] || 0) + 1;
      var done = stub.polls[pid] > stub.pollsBeforeToken;
      return json(200, { id: pid, code: "CODE" + pid + "x9Q2", authToken: done ? CFG.adminToken : null });
    }
    if (method === "GET" && p === "/api/v2/resources") return json(200, stub.resources || RESOURCES);
    if (method === "GET" && (m = p.match(/^\/api\/servers\/([^/]+)\/shared_servers$/))) {
      if (stub.sharedStatus !== 200) {
        return answer(stub.sharedStatus, "<html><body>Internal Server Error</body></html>", "text/html");
      }
      return xml(200, m[1] === SERVER_ID ? SHARED_STOUT : SHARED_NOBODY);
    }
    if (method === "GET" && p === "/api/home/users") {
      return stub.homeStatus === 200 ? xml(200, HOME_XML)
        : answer(stub.homeStatus, "<html><body>Bad Gateway</body></html>", "text/html");
    }
    rec.other.push("unexpected plex.tv call " + method + " " + p);
    return json(404, { error: "unexpected" });
  }

  // ---- the stubs: plex.tv only ----------------------------------------
  window.fetch = function (input, init) {
    init = init || {};
    var url = typeof input === "string" ? input : input && input.url;
    var u = new URL(url, location.href);
    if (u.hostname === "plex.tv") return plexAnswer(u, init);
    if (u.origin === location.origin) {
      rec.same.push({ seq: ++seq, method: String(init.method || "GET").toUpperCase(),
                      path: u.pathname + u.search,
                      body: typeof init.body === "string" ? init.body : "",
                      headers: JSON.stringify(init.headers || {}), step: step });
      return realFetch(input, init);
    }
    rec.other.push(String(url));
    return Promise.reject(new TypeError("Failed to fetch"));
  };

  window.open = function (url, name, features) {
    rec.opens.push({ seq: ++seq, url: String(url), name: String(name || ""),
                     features: String(features || ""), inClick: inClick, blocked: stub.blockPopup });
    if (stub.blockPopup) return null;
    var w = { closed: false, opener: window };
    w.close = function () { if (!w.closed) rec.closes++; w.closed = true; };
    w.location = {};
    Object.defineProperty(w.location, "href", {
      get: function () { return "about:blank"; },
      set: function (v) { rec.navs.push({ seq: ++seq, url: String(v), openerCut: w.opener === null }); }
    });
    stub.popup = w;
    return w;
  };

  Date.now = function () { return realNow.call(Date) + nowOffset; };
  window.alert = function (m) { rec.alerts.push(String(m)); };
  window.confirm = function () { return true; };
  ["log", "info", "warn", "error", "debug"].forEach(function (k) {
    var orig = console[k];
    console[k] = function () {
      rec.consoleCalls++;
      var s = Array.prototype.map.call(arguments, function (a) {
        try { return typeof a === "string" ? a : JSON.stringify(a); } catch (e) { return String(a); }
      }).join(" ");
      if (secretIn(s)) rec.consoleHits.push(s.slice(0, 160));
      return orig.apply(console, arguments);
    };
  });
  window.addEventListener("error", function (e) { rec.errors.push(String(e.message)); });
  window.addEventListener("unhandledrejection", function (e) {
    rec.errors.push("unhandled rejection: " + String(e.reason && (e.reason.message || e.reason)));
  });
  document.addEventListener("securitypolicyviolation", function (e) {
    rec.csp.push({ blockedURI: e.blockedURI, directive: e.effectiveDirective });
  });

  // ---- helpers ----------------------------------------------------------
  function secretIn(s) { return !!whereSecret(s); }
  // "" when s holds none of this run's secrets; else which one, and where.
  function whereSecret(s) {
    s = String(s);
    var all = [CFG.adminToken].concat(Object.keys(K).map(function (k) { return K[k]; }));
    for (var i = 0; i < all.length; i++) {
      var at = s.indexOf(all[i]);
      if (at >= 0) return all[i].slice(0, 22) + "… in: " + s.slice(Math.max(0, at - 80), at);
    }
    return "";
  }
  function canon(v) {
    if (Array.isArray(v)) return "[" + v.map(canon).join(",") + "]";
    if (v && typeof v === "object") {
      return "{" + Object.keys(v).sort().map(function (k) {
        return JSON.stringify(k) + ":" + canon(v[k]);
      }).join(",") + "}";
    }
    return JSON.stringify(v);
  }
  function ok(name, cond, detail) {
    checks.push({ name: name, ok: !!cond, detail: cond ? "" : String(detail === undefined ? "" : detail) });
  }
  function same(name, got, want) {
    var g = canon(got), w = canon(want);
    ok(name, g === w, "got " + g + " want " + w);
  }
  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  async function waitFor(what, fn, ms) {
    var end = performance.now() + (ms || 20000);
    for (;;) {
      var v = null;
      try { v = fn(); } catch (e) { v = null; }
      if (v) return v;
      if (performance.now() > end) throw new Error("timed out waiting for " + what + " (step " + step + ")");
      await sleep(50);
    }
  }
  function $(id) { return document.getElementById(id); }
  function visible(el) { return !!el && el.getClientRects().length > 0; }
  function act(name) { return document.querySelector('#acct [data-act="' + name + '"]'); }
  function press(el) {
    if (!el) throw new Error("no button to press (step " + step + ")");
    inClick = true;
    try { el.click(); } finally { inClick = false; }
  }
  function setType(v) {
    $("sf-type").value = v;
    $("sf-type").dispatchEvent(new Event("change", { bubbles: true }));
  }
  function pickerText() { return $("sf-plex") ? $("sf-plex").textContent : ""; }
  function radios(name) {
    return Array.prototype.slice.call(document.querySelectorAll('#sf-plex input[name="' + name + '"]'));
  }
  function rowOf(input) {
    var wrap = input.parentNode.querySelector(".wrap");
    var tag = wrap.querySelector(".tag"), note = wrap.querySelector(".meta");
    return { input: input, name: wrap.firstChild ? wrap.firstChild.nodeValue : "",
             tag: tag ? tag.textContent : "", note: note ? note.textContent : "",
             off: input.disabled, text: wrap.textContent };
  }
  function people() { return radios("sf-plex-person").map(rowOf); }
  function servers() { return radios("sf-plex-server").map(rowOf); }
  function person(name) {
    var r = people().filter(function (p) { return p.name === name; })[0];
    if (!r) throw new Error("no person " + name + " (step " + step + ")");
    return r;
  }
  function server(name) {
    var r = servers().filter(function (s) { return s.name === name; })[0];
    if (!r) throw new Error("no server " + name + " (step " + step + ")");
    return r;
  }
  function plexWho() {
    var el = document.querySelector('#acct-services [data-plex="who"]');
    return el ? el.textContent : "";
  }
  function plexUrl() {
    var el = document.querySelector('#acct-services [data-plex="url"]');
    return el ? el.textContent : "";
  }
  function sent(method, part, from) {
    for (var i = from; i < rec.same.length; i++) {
      var r = rec.same[i];
      if (r.method === method && r.path.indexOf(part) >= 0 && r.path.indexOf("/order") < 0) return r;
    }
    return null;
  }
  // The page's Plex client id: as stored, or with storage blocked, as the
  // page sent it on its first plex.tv call.
  function clientId() {
    if (CFG.noStorage) return rec.plex.length ? rec.plex[0].headers["x-plex-client-identifier"] || "" : "";
    try { return localStorage.getItem(CLIENT_KEY) || ""; } catch (e) { return ""; }
  }
  function authURL(code) {
    return "https://app.plex.tv/auth#?clientID=" + encodeURIComponent(clientId()) +
      "&code=" + encodeURIComponent(code) + "&context%5Bdevice%5D%5Bproduct%5D=Alexandria%20Companion";
  }
  function pinPolls() {
    return rec.plex.filter(function (r) { return /^\/api\/v2\/pins\/\d+$/.test(r.path); }).length;
  }
  function plexPick(server, userId, title) {
    return { serverId: server === "hostile" ? HOSTILE_ID : SERVER_ID,
             serverName: server === "hostile" ? HOSTILE : "StoutOffice",
             userId: userId, userTitle: title,
             connections: STOUT_CONNS.map(function (c) {
               return { uri: c.uri, local: c.local, relay: c.relay };
             }) };
  }
  async function checkpoint(name) {
    var r = await realFetch("/__flow/checkpoint/" + name, { method: "POST" });
    if (r.status !== 204) throw new Error("checkpoint " + name + " answered " + r.status);
  }
  async function openForm() {
    press(act("add"));
    await waitFor("the add form", function () { return $("sf-type"); });
  }
  async function signInToPlex() {
    var opens = rec.opens.length, plexCalls = rec.plex.length;
    press(act("plex-signin"));
    return { open: rec.opens[opens], firstCall: rec.plex[plexCalls] };
  }
  async function untilServers(n) {
    await waitFor(n + " servers listed", function () { return servers().length === n; });
  }
  async function untilPeople() {
    await waitFor("the people list", function () { return people().length > 0; });
  }

  // ---- the run -------------------------------------------------------------
  async function run() {
    step = "sign-in";
    await waitFor("the sign-in screen", function () { return !$("login").hidden; });
    $("use-recovery").click();
    $("password").value = CFG.password;
    $("code").value = CFG.recovery;
    $("signin").click();
    await waitFor("the signed-in page", function () {
      return !$("app").hidden && document.querySelector('[data-signin="' + CFG.grandmaId + '"]');
    });

    // A. Every other service: the form behaves as before.
    step = "A";
    document.querySelector('[data-signin="' + CFG.grandmaId + '"]').click();
    await waitFor("Grandma's panel", function () { return act("add"); });
    await openForm();
    ok("A1 the add form starts on Audiobookshelf", $("sf-type").value === "abs");
    ok("A2 Audiobookshelf: Address shown with its own example",
       visible($("sf-url")) && $("sf-url").placeholder === "https://abs.example.com", $("sf-url").placeholder);
    ok("A3 Audiobookshelf: How they sign in, username and password shown",
       visible($("sf-kind")) && visible($("sf-user")) && visible($("sf-pass")));
    ok("A4 Audiobookshelf: no Plex picker", !visible($("sf-plex")) && $("sf-plex").hidden);
    setType("jellyfin");
    ok("A5 Jellyfin: its own Address example", $("sf-url").placeholder === "https://jellyfin.example.com",
       $("sf-url").placeholder);
    ok("A6 Jellyfin: its hint unchanged", $("sf-hint").textContent ===
       "Use their own Jellyfin login, never a dashboard API key (those are admin-wide).");
    setType("komga");
    ok("A7 Komga: its own Address example, not an ABS one",
       $("sf-url").placeholder === "https://komga.example.com", $("sf-url").placeholder);
    setType("plex");
    ok("A8 Plex: no Address", !visible($("sf-url")) && $("sf-classic").hidden);
    ok("A9 Plex: no How they sign in", !visible($("sf-kind")));
    ok("A10 Plex: no username, password, token or API key boxes",
       !visible($("sf-user")) && !visible($("sf-pass")) && !visible($("sf-token")) && !visible($("sf-key")));
    ok("A11 Plex: the picker, starting with Sign in with Plex",
       visible($("sf-plex")) && act("plex-signin") && act("plex-signin").textContent === "Sign in with Plex");
    ok("A12 Plex: the new hint", $("sf-hint").textContent.indexOf(
       "Sign in with Plex, pick your server, then the person") === 0, $("sf-hint").textContent);
    ok("A13 the old Plex hint is gone", document.body.textContent.indexOf("Plex Home users: a token") < 0);
    ok("A14 no Plex friend in the sign-in choices",
       Array.prototype.every.call($("sf-kind").options, function (o) { return o.text.indexOf("Plex") < 0; }));
    setType("abs");
    ok("A15 back to Audiobookshelf: Address and sign-in back, picker gone",
       visible($("sf-url")) && visible($("sf-kind")) && !visible($("sf-plex")));
    $("sf-url").value = "http://abs.local:13378";
    $("sf-user").value = "grandma";
    $("sf-pass").value = CFG.absPassword;
    var from = rec.same.length;
    press(act("save"));
    await waitFor("the ABS service listed", function () {
      return !$("sf-type") && $("acct-services").textContent.indexOf("abs.local") >= 0;
    });
    var absPost = sent("POST", "/services", from);
    same("A16 Audiobookshelf is sent exactly as before", absPost && JSON.parse(absPost.body),
         { type: "abs", name: "Audiobookshelf", baseUrl: "http://abs.local:13378", loginKind: "password",
           username: "grandma", password: CFG.absPassword });

    // M. A saved login never moves to a new address (security sweep
    // 2026-10, S1): the form says so as the address changes, and Save asks
    // for the password before sending anything. Nothing is saved here.
    step = "M";
    press(document.querySelector('#acct-services [data-act="edit"]'));
    await waitFor("the ABS edit form", function () { return $("sf-type") && $("sf-type").value === "abs"; });
    ok("M1 editing in place: an empty box keeps the saved login",
       visible($("sf-keep")) && !visible($("sf-moved")) &&
       $("sf-pass").placeholder === "Saved — leave empty to keep it", $("sf-pass").placeholder);
    $("sf-url").value = "https://somewhere-else.example";
    $("sf-url").dispatchEvent(new Event("input", { bubbles: true }));
    ok("M2 a new address: the keep promise goes, the box asks for the password",
       !visible($("sf-keep")) && visible($("sf-moved")) && $("sf-pass").placeholder === "Password",
       $("sf-pass").placeholder);
    from = rec.same.length;
    press(act("save"));
    await sleep(300);
    ok("M3 Save without it: asked for the password, nothing sent",
       $("toast").textContent === "Enter their password. A saved login never moves to a new address." &&
       !sent("PATCH", "/services/", from), $("toast").textContent);
    $("sf-url").value = "HTTP://ABS.local:13378/";
    $("sf-url").dispatchEvent(new Event("input", { bubbles: true }));
    ok("M4 the same place written another way: the keep promise is back",
       visible($("sf-keep")) && !visible($("sf-moved")) &&
       $("sf-pass").placeholder === "Saved — leave empty to keep it", $("sf-pass").placeholder);
    press(act("cancel-form"));
    await waitFor("the form closed", function () { return !$("sf-type"); });

    // B. Add Plex by picking the person.
    step = "B";
    await openForm();
    setType("plex");
    stub.resources = RESOURCES;
    stub.pollsBeforeToken = 1;
    var b = await signInToPlex();
    ok("B1 Plex's window opens inside the click, blank first",
       b.open && b.open.inClick && b.open.url === "about:blank", JSON.stringify(b.open));
    ok("B2 ...before anything is asked of plex.tv",
       b.open && b.firstCall && b.open.seq < b.firstCall.seq && b.firstCall.method === "POST" &&
       b.firstCall.path === "/api/v2/pins" && b.firstCall.search === "?strong=true",
       JSON.stringify(b.firstCall));
    var navs = rec.navs.length;
    await waitFor("Plex's sign-in page in the window", function () { return rec.navs.length > navs; });
    var nav = rec.navs[navs];
    ok("B3 the window goes to Plex's sign-in page for this PIN",
       nav.url === authURL("CODE" + stub.pin + "x9Q2"), nav.url + " vs " + authURL("CODE" + stub.pin + "x9Q2"));
    ok("B4 the window is cut off from this page before Plex's page loads", nav.openerCut === true);
    ok("B5 while waiting: says so, with a way to cancel that isn't just \"Cancel\"",
       pickerText().indexOf("Waiting for you to sign in on Plex's page") >= 0 && act("plex-cancel") &&
       act("plex-cancel").textContent === "Cancel Plex sign-in");
    await untilServers(2);
    ok("B6 the window closes once signed in", stub.popup && stub.popup.closed);
    var pinCalls = rec.plex.filter(function (r) { return r.step === "B" && r.path.indexOf("/api/v2/pins") === 0; });
    ok("B7 polls the PIN every 2 seconds",
       pinCalls.length === 3 && pinCalls[1].t - pinCalls[0].t >= 1900 && pinCalls[2].t - pinCalls[1].t >= 1900,
       JSON.stringify(pinCalls.map(function (r) { return Math.round(r.t); })));
    same("B8 two owned servers offered (no player, no server shared with the admin)",
         servers().map(function (s) { return s.name; }).sort(), [HOSTILE, "StoutOffice"].sort());
    ok("B9 a hostile server name is plain text", document.getElementsByTagName("img").length === 0 &&
       rec.alerts.length === 0 && servers().some(function (s) { return s.name === HOSTILE; }));
    press(server("StoutOffice").input);
    await untilPeople();
    var rows = people();
    same("B10 Home first, then friends (no key: not listed)",
         rows.map(function (r) { return r.tag; }), ["Home", "Home", "Friend", "Friend"]);
    same("B11 who is listed", rows.map(function (r) { return r.name; }).slice(0, 2).sort()
         .concat(rows.slice(2).map(function (r) { return r.name; })),
         [HOSTILE, "Grandma"].sort().concat(["bobsmith", "carol"]));
    ok("B12 someone who hasn't accepted is listed, says so, and can't be picked",
       person("carol").off && person("carol").note === " — hasn't accepted your share yet");
    ok("B13 the others can be picked", !person("Grandma").off && !person("bobsmith").off && !person(HOSTILE).off);
    ok("B14 the owner line", pickerText().indexOf(
       "You, the owner, aren't listed — sign in to Plex in the app as usual.") >= 0);
    ok("B15 a hostile Home title is plain text", document.getElementsByTagName("img").length === 0 &&
       rec.alerts.length === 0 && person(HOSTILE).name === HOSTILE);
    var html = document.documentElement.outerHTML;
    ok("B16 neither the admin's Plex token nor any person's key is in the page", !secretIn(html),
       whereSecret(html));
    press(person("Grandma").input);
    from = rec.same.length;
    press(act("save"));
    await waitFor("the Plex service listed", function () {
      return !$("sf-type") && plexWho() === "Plex: Grandma on StoutOffice";
    });
    var addPost = sent("POST", "/services", from);
    same("B17 sent: the person's key and the pick, nothing else",
         addPost && JSON.parse(addPost.body),
         { type: "plex", name: "Plex", loginKind: "plexuser", token: K.grandma,
           plex: plexPick("stout", 42, "Grandma") });
    ok("B18 the list says who, on which server", plexWho() === "Plex: Grandma on StoutOffice", plexWho());
    ok("B19 the list shows the address the server chose (remote https)",
       plexUrl() === "https://203-0-113-7.abc123.plex.direct:32400", plexUrl());
    step = "checkpoint-added";
    await checkpoint("added");

    // C. Rename: the pick is kept.
    step = "C";
    press(document.querySelector('#acct-services [data-plex-card] [data-act="edit"]'));
    await waitFor("the edit form", function () { return $("sf-type") && $("sf-type").value === "plex"; });
    ok("C1 editing Plex: no Address or sign-in boxes", !visible($("sf-url")) && !visible($("sf-kind")));
    ok("C2 editing Plex: says who it signs in as, with Pick again",
       pickerText().indexOf("Signs in as Grandma on StoutOffice.") >= 0 &&
       act("plex-signin").textContent === "Pick again");
    $("sf-name").value = "Grandma's Plex";
    from = rec.same.length;
    var plexBefore = rec.plex.length;
    press(act("save"));
    await waitFor("the new name listed", function () {
      return !$("sf-type") && $("acct-services").textContent.indexOf("Grandma's Plex") >= 0;
    });
    var rename = sent("PATCH", "/services/", from);
    ok("C3 a rename sends just the name", rename && rename.body === JSON.stringify({ name: "Grandma's Plex" }),
       rename && rename.body);
    ok("C4 a rename asks nothing of plex.tv", rec.plex.length === plexBefore);
    ok("C5 the pick is kept", plexWho() === "Plex: Grandma on StoutOffice", plexWho());
    step = "checkpoint-renamed";
    await checkpoint("renamed");

    // D. Pick again: a fresh sign-in, a new person, their key.
    step = "D";
    press(document.querySelector('#acct-services [data-plex-card] [data-act="edit"]'));
    await waitFor("the edit form", function () { return $("sf-type") && $("sf-type").value === "plex"; });
    var d = await signInToPlex();
    ok("D1 Pick again starts a fresh Plex sign-in (a new PIN, no token)",
       d.firstCall && d.firstCall.method === "POST" && d.firstCall.path === "/api/v2/pins" &&
       !d.firstCall.headers["x-plex-token"], JSON.stringify(d.firstCall));
    await untilServers(2);
    press(server("StoutOffice").input);
    await untilPeople();
    press(person("bobsmith").input);
    from = rec.same.length;
    press(act("save"));
    await waitFor("the new pick listed", function () {
      return !$("sf-type") && plexWho() === "Plex: bobsmith on StoutOffice";
    });
    var repick = sent("PATCH", "/services/", from);
    same("D2 picking again sends the name, the new pick and its key",
         repick && JSON.parse(repick.body),
         { name: "Grandma's Plex", plex: plexPick("stout", 1002, "bobsmith"), token: K.bob });
    step = "checkpoint-repicked";
    await checkpoint("repicked");

    // E. A blocked window, page hide, and cancel.
    step = "E";
    await openForm();
    setType("plex");
    stub.blockPopup = true;
    stub.pollsBeforeToken = 1;
    await signInToPlex();
    await waitFor("the blocked-window link", function () { return document.querySelector("#sf-plex a"); });
    var link = document.querySelector("#sf-plex a");
    ok("E1 a blocked window: a link to Plex's sign-in instead",
       pickerText().indexOf("Your browser blocked Plex's window.") >= 0 &&
       link.href === authURL("CODE" + stub.pin + "x9Q2") && link.target === "_blank" &&
       link.rel === "noopener noreferrer", link.href + " " + link.target + " " + link.rel);
    await untilServers(2);
    window.dispatchEvent(new PageTransitionEvent("pagehide", { persisted: false }));
    ok("E2 page hide ends the sign-in: back to the start",
       act("plex-signin") && act("plex-signin").textContent === "Sign in with Plex" &&
       servers().length === 0 && !act("plex-cancel"));
    stub.blockPopup = false;
    stub.pollsBeforeToken = 1000;
    var e3 = await signInToPlex();
    ok("E3 after page hide, signing in starts over (a new PIN, no token)",
       e3.firstCall && e3.firstCall.path === "/api/v2/pins" && !e3.firstCall.headers["x-plex-token"]);
    var polled = pinPolls();
    await waitFor("a PIN poll", function () { return pinPolls() > polled; }, 6000);
    press(act("plex-cancel"));
    ok("E4 Cancel: back to the start", act("plex-signin") && !act("plex-cancel") &&
       act("plex-signin").textContent === "Sign in with Plex");
    ok("E5 Cancel closes Plex's window", stub.popup && stub.popup.closed);
    polled = pinPolls();
    await sleep(4500);
    ok("E6 Cancel stops asking Plex", pinPolls() === polled, pinPolls() + " vs " + polled);
    press(act("cancel-form"));

    // T. Five minutes without a sign-in.
    step = "T";
    await openForm();
    setType("plex");
    stub.pollsBeforeToken = 1000;
    polled = pinPolls();
    await signInToPlex();
    await waitFor("a PIN poll", function () { return pinPolls() > polled; }, 6000);
    nowOffset = 5 * 60 * 1000 + 1000;
    await waitFor("the time-out", function () {
      return pickerText().indexOf("The Plex sign-in timed out. Start again when you're ready.") >= 0;
    }, 6000);
    ok("T1 after five minutes: timed out, in plain words, with Try again", !!act("plex-retry"));
    polled = pinPolls();
    await sleep(4500);
    ok("T2 ...and stops asking Plex", pinPolls() === polled);
    nowOffset = 0;
    press(act("cancel-form"));

    // F. An account that owns no server.
    step = "F";
    await openForm();
    setType("plex");
    stub.resources = NO_OWNED;
    stub.pollsBeforeToken = 1;
    await signInToPlex();
    await waitFor("the no-server message", function () {
      return pickerText().indexOf("This Plex account owns no server. Sign in as the server's owner.") >= 0;
    });
    ok("F1 no owned server: says so, offers Sign in again",
       act("plex-signin") && act("plex-signin").textContent === "Sign in again" && servers().length === 0);
    press(act("cancel-form"));

    // S. One owned server: chosen for the admin.
    step = "S";
    await openForm();
    setType("plex");
    stub.resources = [RESOURCES[0], RESOURCES[2], RESOURCES[3]];
    await signInToPlex();
    await untilPeople();
    ok("S1 one owned server: picked for the admin, straight to its people",
       servers().length === 0 && people().length === 4 && rec.plex.some(function (r) {
         return r.step === "S" && r.path === "/api/servers/" + SERVER_ID + "/shared_servers";
       }), servers().length + " servers, " + people().length + " people");
    press(act("cancel-form"));
    stub.resources = RESOURCES;

    // G. Errors in plain words; nobody to pick.
    step = "G";
    await openForm();
    setType("plex");
    stub.resources = RESOURCES;
    stub.sharedStatus = 500;
    await signInToPlex();
    await untilServers(2);
    press(server("StoutOffice").input);
    await waitFor("the error", function () { return act("plex-retry"); });
    var err = document.querySelector('#sf-plex [role="alert"]');
    ok("G1 plex.tv failing: plain words, no raw error",
       err && err.textContent === "Plex had a problem (error 500). Try again in a moment." &&
       !/Error|TypeError|undefined|\bat\b/.test(err.textContent), err && err.textContent);
    stub.sharedStatus = 200;
    stub.homeStatus = 502;
    press(act("plex-retry"));
    await untilPeople();
    ok("G2 Try again loads the list; without the Home list nobody is labelled",
       people().every(function (p) { return p.tag === ""; }) &&
       people().some(function (p) { return p.name === "Plex user 42"; }),
       JSON.stringify(people().map(function (p) { return [p.name, p.tag]; })));
    stub.homeStatus = 200;
    press(person("carol").input);
    from = rec.same.length;
    press(act("save"));
    ok("G3 Save with nobody picked (carol can't be): says so where the admin looks",
       pickerText().indexOf("Pick who this is for first.") >= 0 && sent("POST", "/services", from) === null);
    press(server(HOSTILE).input);
    await waitFor("the unshared server", function () { return pickerText().indexOf("isn't shared with anyone yet") >= 0; });
    ok("G4 a server shared with nobody: says so, as plain text, with Check again",
       pickerText().indexOf(HOSTILE + " isn't shared with anyone yet. Share it in Plex, then check again.") >= 0 &&
       act("plex-retry") && act("plex-retry").textContent === "Check again" &&
       document.getElementsByTagName("img").length === 0);
    var callsAtClose = rec.plex.length;
    press(act("cancel-form"));
    await openForm();
    setType("plex");
    ok("G5 closing the form ends the sign-in: a new form starts from scratch",
       act("plex-signin") && act("plex-signin").textContent === "Sign in with Plex" &&
       servers().length === 0 && people().length === 0 && rec.plex.length === callsAtClose);
    press(act("cancel-form"));

    // H. An old Plex service becomes a picked person.
    step = "H";
    document.querySelector('[data-signin="' + CFG.oldId + '"]').click();
    await waitFor("the old service's panel", function () {
      return document.querySelector('#acct-services [aria-label="Edit Plex (old)"]');
    });
    ok("H0 the old service lists as before (its address, its token)",
       $("acct-services").textContent.indexOf("https://plex.example.com:32400") >= 0 &&
       $("acct-services").textContent.indexOf("Token · login saved") >= 0);
    press(document.querySelector('#acct-services [aria-label="Edit Plex (old)"]'));
    await waitFor("the edit form", function () { return $("sf-type") && $("sf-type").value === "plex"; });
    var note = document.querySelector("#sf-plex .note");
    ok("H1 editing an old Plex service: says it was set up the old way, with the picker",
       note && note.textContent.indexOf("Set up the old way") === 0 && act("plex-signin") &&
       act("plex-signin").textContent === "Sign in with Plex", note && note.textContent);
    await signInToPlex();
    await untilServers(2);
    press(server("StoutOffice").input);
    await untilPeople();
    press(person(HOSTILE).input);
    from = rec.same.length;
    press(act("save"));
    await waitFor("the converted service listed", function () {
      return !$("sf-type") && plexWho() === "Plex: " + HOSTILE + " on StoutOffice";
    });
    var convert = sent("PATCH", "/services/", from);
    same("H2 converting sends the pick, its key, and clears the old login",
         convert && JSON.parse(convert.body),
         { type: "plex", name: "Plex (old)", loginKind: "plexuser", token: K.hostile,
           plex: plexPick("stout", 43, HOSTILE), username: "", password: "", apiKey: "" });
    ok("H3 the list shows the hostile name as plain text",
       plexWho() === "Plex: " + HOSTILE + " on StoutOffice" && document.getElementsByTagName("img").length === 0);
    step = "checkpoint-converted";
    await checkpoint("converted");

    // Z. Over the whole run.
    step = "Z";
    var cid = clientId();
    var uuid4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
    if (CFG.noStorage) {
      ok("Z1 storage blocked: the Plex client id is still a random UUID, one for the whole page",
         uuid4.test(cid), cid);
      var threw = false;
      try { void localStorage.length; } catch (e) { threw = true; }
      ok("Z2 ...and storage really was blocked for this run", threw);
    } else {
      ok("Z1 the Plex client id is a random UUID kept in this browser", uuid4.test(cid), cid);
      var keys = [];
      try { for (var i = 0; i < localStorage.length; i++) keys.push(localStorage.key(i)); } catch (e) { keys.push("?"); }
      same("Z2 local storage holds the client id and nothing else", keys, [CLIENT_KEY]);
      var ss = -1;
      try { ss = sessionStorage.length; } catch (e) { ss = -1; }
      ok("Z3 session storage is empty", ss === 0, ss);
    }
    var stored = [];
    try {
      for (var j = 0; j < localStorage.length; j++) stored.push(localStorage.getItem(localStorage.key(j)));
      for (var m = 0; m < sessionStorage.length; m++) stored.push(sessionStorage.getItem(sessionStorage.key(m)));
    } catch (e) { stored.push("storage unreadable"); }
    var everywhere = "cookie: " + document.cookie + " storage: " + stored.join(" ") +
      " page: " + document.documentElement.outerHTML;
    ok("Z4 no secret in cookies, storage or the page", !secretIn(everywhere), whereSecret(everywhere));
    var leaks = rec.same.filter(function (r) { return r.path.indexOf(CFG.adminToken) >= 0 ||
      r.body.indexOf(CFG.adminToken) >= 0 || r.headers.indexOf(CFG.adminToken) >= 0; });
    ok("Z5 the admin's Plex token never reached this server (" + rec.same.length + " requests)",
       leaks.length === 0, JSON.stringify(leaks));
    var keyed = rec.same.filter(function (r) { return secretIn(r.path + r.body + r.headers); });
    ok("Z6 people's keys reached this server only in the three picks saved",
       keyed.length === 3 && keyed.every(function (r) {
         return (r.method === "POST" || r.method === "PATCH") && r.path.indexOf("/services") >= 0;
       }), JSON.stringify(keyed.map(function (r) { return r.method + " " + r.path; })));
    ok("Z7 nothing written to the console", rec.consoleCalls === 0 && rec.consoleHits.length === 0,
       rec.consoleCalls + " calls, " + rec.consoleHits.length + " with a secret");
    var bad = rec.plex.filter(function (r) {
      var h = r.headers, pin = r.path.indexOf("/api/v2/pins") === 0;
      var wantAccept = r.path.indexOf("/api/v2/") === 0 ? "application/json" : "application/xml";
      return h["x-plex-client-identifier"] !== cid || h["x-plex-product"] !== "Alexandria Companion" ||
        h["x-plex-version"] !== "1.0" || h["x-plex-platform"] !== "Web" ||
        h["x-plex-device-name"] !== "Alexandria admin page" || h.accept !== wantAccept ||
        r.credentials !== "omit" ||
        (pin ? "x-plex-token" in h : h["x-plex-token"] !== CFG.adminToken) ||
        r.search.indexOf(CFG.adminToken) >= 0;
    });
    ok("Z8 every plex.tv call (" + rec.plex.length + "): this browser's id, the product headers, " +
       "the right Accept, no cookies, and the admin's token as a header on signed-in calls only",
       rec.plex.length > 0 && bad.length === 0, JSON.stringify(bad.slice(0, 3)));
    ok("Z9 nothing reached for but this server and plex.tv", rec.other.length === 0, JSON.stringify(rec.other));
    ok("Z10 no alert, no <img>, no page error",
       rec.alerts.length === 0 && document.getElementsByTagName("img").length === 0 && rec.errors.length === 0,
       JSON.stringify({ alerts: rec.alerts, errors: rec.errors }));

    // The Content-Security-Policy as Chrome enforces it, with the real
    // fetch (no stub). This test resolves no outside host, so nothing
    // leaves the machine either way: plex.tv fails at DNS, the rest are
    // refused by the policy before any lookup.
    rec.csp = [];
    var probes = ["https://plex.tv/__flow_csp_probe", "https://app.plex.tv/__flow_csp_probe",
                  "https://www.plex.tv/__flow_csp_probe", "https://example.com/__flow_csp_probe"];
    await Promise.all(probes.map(function (u) {
      return realFetch(u, { credentials: "omit" }).then(function () { return 1; }, function () { return 0; });
    }));
    await sleep(500);
    var refused = function (origin) {
      return rec.csp.some(function (v) { return v.blockedURI.indexOf(origin) === 0 && v.directive === "connect-src"; });
    };
    ok("Z11 the policy lets the page reach https://plex.tv", !refused("https://plex.tv"), JSON.stringify(rec.csp));
    ok("Z12 ...and refuses app.plex.tv, www.plex.tv and any other host",
       refused("https://app.plex.tv") && refused("https://www.plex.tv") && refused("https://example.com"),
       JSON.stringify(rec.csp));
    return cid;
  }

  document.addEventListener("DOMContentLoaded", function () {
    var cid = "";
    run().then(function (c) { cid = c; }, function (e) {
      ok("the run finished", false, (e && e.message) || String(e));
    }).then(function () {
      return realFetch("/__flow/report", {
        method: "POST",
        body: JSON.stringify({ checks: checks, clientId: cid, plexCalls: rec.plex.length,
                               serverCalls: rec.same.length })
      });
    });
  });
})();
