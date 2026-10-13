// Server Admin sign-ins on the admin page. TEST-ONLY: the flow harness
// proxy (../flow/main.go, started by run.sh) puts it at the top of the
// page's <head>, ahead of the page's own script. The real server never
// serves it. Nothing is stubbed: every request goes to the real server.
//
// It signs in with a recovery code, saves a Grimmory sign-in through the
// form (a hostile username), pauses while run.sh checks the server with
// curl, removes it, and reports what it saw to /__flow/report.
(function () {
  "use strict";
  var CFG = window.__FLOW__;
  // The run's test secrets, out of the page once read, so the check for
  // the password on the page sees only what the page put there.
  if (document.currentScript) document.currentScript.remove();
  try { delete window.__FLOW__; } catch (e) { window.__FLOW__ = undefined; }
  var realFetch = window.fetch.bind(window);
  var checks = [], alerts = [], step = "start";
  window.alert = function (m) { alerts.push(String(m)); };
  window.confirm = function () { return true; };

  function ok(name, pass, detail) {
    checks.push({ name: name, ok: !!pass, detail: pass ? "" : String(detail || "") });
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
  function listText() { return $("svc-admins").textContent; }
  async function checkpoint(name) {
    var r = await realFetch("/__flow/checkpoint/" + name, { method: "POST" });
    if (r.status !== 204) throw new Error("checkpoint " + name + " answered " + r.status);
  }

  async function run() {
    step = "sign-in";
    await waitFor("the sign-in screen", function () { return !$("login").hidden; });
    $("use-recovery").click();
    $("password").value = CFG.password;
    $("code").value = CFG.recovery;
    $("signin").click();
    await waitFor("the signed-in page", function () { return !$("app").hidden; });

    step = "empty";
    await waitFor("the empty list", function () { return /None kept yet/.test(listText()); });
    ok("the section starts empty with its form", !$("svc-admin-form").hidden, "form hidden");

    step = "save";
    $("svc-admin-type").value = "booklore";
    $("svc-admin-url").value = "http://grimmory.lan:6060/";
    $("svc-admin-user").value = CFG.hostile;
    $("svc-admin-pass").value = CFG.grimmoryPassword;
    $("svc-admin-form").querySelector('button[type="submit"]').click();
    await waitFor("the saved row", function () { return /Grimmory/.test(listText()); });
    ok("the row names the service and the address, trailing slash gone",
       listText().indexOf("http://grimmory.lan:6060") >= 0 &&
       listText().indexOf("6060/") < 0, listText());
    ok("a hostile username shows as text, not markup",
       listText().indexOf(CFG.hostile) >= 0 && !document.querySelector("#svc-admins img"),
       listText());
    ok("no alert ran", alerts.length === 0, alerts.join(" | "));
    ok("the password field is empty after saving", $("svc-admin-pass").value === "", "still filled");
    ok("the password is nowhere on the page",
       document.documentElement.innerHTML.indexOf(CFG.grimmoryPassword) < 0, "found on the page");
    await checkpoint("saved");

    step = "remove";
    var remove = document.querySelector("#svc-admins [data-remove-svc-admin]");
    ok("the row has a Remove button", !!remove, "none");
    remove.click();
    await waitFor("the empty list again", function () { return /None kept yet/.test(listText()); });
    await checkpoint("removed");
  }

  document.addEventListener("DOMContentLoaded", function () {
    run().catch(function (e) {
      ok("the run finished", false, (e && e.message) || String(e));
    }).then(function () {
      return realFetch("/__flow/report", {
        method: "POST", body: JSON.stringify({ checks: checks })
      });
    });
  });
})();
