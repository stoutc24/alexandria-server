// Making a person's account on a media server from the admin page.
// TEST-ONLY: the flow harness proxy (../flow/main.go, started by run.sh)
// puts it at the top of the page's <head>. Nothing is stubbed but
// window.confirm (counted, answered yes): every request goes to the real
// server, which calls run.sh's small Audiobookshelf.
//
// Emily gets an Audiobookshelf service with the "create this account"
// tick, and ABS's libraries listed under it: one is unticked, and her ABS
// account is made seeing only the other. Then her service is edited with a
// new password and the tick: the page asks (her libraries stay as they
// are), and her ABS password changes. Noah's account is refused with no
// library ticked, then made with all of them ticked: every library.
(function () {
  "use strict";
  var CFG = window.__FLOW__;
  if (document.currentScript) document.currentScript.remove();
  try { delete window.__FLOW__; } catch (e) { window.__FLOW__ = undefined; }
  var realFetch = window.fetch.bind(window);
  var checks = [], asked = [], step = "start";
  window.confirm = function (m) { asked.push(String(m)); return true; };

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
  function visible(el) { return !!el && el.getClientRects().length > 0; }
  function act(name) { return document.querySelector('#acct [data-act="' + name + '"]'); }
  function card(id) { return document.querySelector('[data-person="' + id + '"]'); }
  function press(el) {
    if (!el) throw new Error("no button to press (step " + step + ")");
    el.click();
  }
  function toastText() { return $("toast").textContent; }
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

    step = "open";
    press(document.querySelector('#areas [data-area="people"]'));
    await waitFor("Emily's card", function () { return card(CFG.emilyId); });
    press(card(CFG.emilyId).querySelector('[data-tab="services"]'));
    await waitFor("the Add a service button", function () { return act("add"); });
    press(act("add"));
    await waitFor("the add form", function () { return $("sf-type"); });
    $("sf-type").value = "abs";
    $("sf-type").dispatchEvent(new Event("change", { bubbles: true }));
    $("sf-url").value = CFG.absUrl;
    $("sf-user").value = "emily";
    $("sf-pass").value = CFG.firstPassword;

    step = "tick";
    await waitFor("the create-account tick", function () { return visible($("sf-prov")); });
    var label = $("sf-prov-label").textContent;
    ok("P1 the tick names the service and where it is",
       label.indexOf("Audiobookshelf") >= 0 && label.indexOf(CFG.absHost) >= 0, label);
    ok("P2 ticked for a new service", $("sf-prov-on").checked, "unticked");
    ok("P3 it says an existing account's password is set", /set its password/.test(label), label);
    ok("P4 ABS: the email box is there, optional",
       visible($("sf-prov-mail")) && /optional/.test($("sf-prov-email-opt").textContent), $("sf-prov-mail").hidden);
    $("sf-kind").value = "token";
    $("sf-kind").dispatchEvent(new Event("change", { bubbles: true }));
    ok("P5 gone for a token login", !visible($("sf-prov")), "still shown");
    $("sf-kind").value = "password";
    $("sf-kind").dispatchEvent(new Event("change", { bubbles: true }));
    ok("P6 back for a password", visible($("sf-prov")), "hidden");

    step = "libraries";
    var boxes = await waitFor("ABS's libraries under the tick", function () {
      var b = $("sf-prov-libs-list").querySelectorAll("input[data-lib]");
      return b.length === 2 ? b : null;
    });
    ok("L1 every library listed, all ticked", boxes[0].checked && boxes[1].checked &&
       boxes[0].getAttribute("data-lib") === "lib-books", boxes.length);
    var listText = $("sf-prov-libs-list").textContent;
    ok("L2 a library's name is text, never markup",
       listText.indexOf("Podcasts <b>") >= 0 && !$("sf-prov-libs-list").querySelector("b"), listText);
    $("sf-prov-on").click();
    ok("L3 unticking the account box hides the libraries", !visible($("sf-prov-libs")), "still shown");
    $("sf-prov-on").click();
    boxes = $("sf-prov-libs-list").querySelectorAll("input[data-lib]");
    ok("L4 ticked again, they are back", visible($("sf-prov-libs")) && boxes.length === 2, boxes.length);
    boxes[1].click();

    step = "create";
    press(act("save"));
    await waitFor("the created toast", function () { return /Account created on Audiobookshelf/.test(toastText()); });
    ok("P7 the toast says it was made, and the service added", /Service added/.test(toastText()), toastText());
    ok("P8 nothing was asked for a new account", asked.length === 0, asked.join(" | "));
    ok("P9 the password is nowhere on the page",
       document.documentElement.innerHTML.indexOf(CFG.firstPassword) < 0, "found");
    await checkpoint("created");

    step = "edit";
    var edit = await waitFor("the service's Edit button", function () {
      return document.querySelector('#acct-services [data-act="edit"]');
    });
    press(edit);
    await waitFor("the edit form", function () { return $("sf-type") && $("sf-prov"); });
    await waitFor("the tick on the edit", function () { return visible($("sf-prov")); });
    ok("P10 not ticked on an edit", !$("sf-prov-on").checked, "ticked");
    $("sf-pass").value = CFG.secondPassword;
    $("sf-prov-on").click();
    var editBoxes = await waitFor("the libraries on the edit", function () {
      var b = $("sf-prov-libs-list").querySelectorAll("input[data-lib]");
      return b.length === 2 ? b : null;
    });
    editBoxes[0].click();
    press(act("save"));
    await waitFor("the reset toast", function () { return /Password changed on Audiobookshelf/.test(toastText()); });
    ok("P11 asked once, naming the account and the service",
       asked.length === 1 && /emily already has an account on Audiobookshelf/.test(asked[0]), asked.join(" | "));
    ok("L5 the question says her libraries stay as they are",
       /libraries stay as they are/.test(asked[0] || ""), asked[0]);
    await checkpoint("reset");

    step = "admin";
    press(act("add"));
    await waitFor("the add form again", function () { return $("sf-type"); });
    $("sf-type").value = "abs";
    $("sf-type").dispatchEvent(new Event("change", { bubbles: true }));
    $("sf-url").value = CFG.absUrl;
    $("sf-user").value = "root";
    $("sf-pass").value = "not-for-root";
    await waitFor("the tick", function () { return visible($("sf-prov")); });
    press(act("save"));
    await waitFor("the refusal", function () { return /admin account on Audiobookshelf/.test(toastText()); });
    ok("P12 an admin account is refused, the form left open", !!$("sf-type"), "form closed");
    ok("P13 no second question", asked.length === 1, asked.join(" | "));
    await checkpoint("admin");

    step = "none-ticked";
    $("sf-user").value = "noah";
    $("sf-pass").value = "noah-pass-1";
    var noah = await waitFor("the libraries for Noah", function () {
      var b = $("sf-prov-libs-list").querySelectorAll("input[data-lib]");
      return b.length === 2 ? b : null;
    });
    noah[0].click();
    noah[1].click();
    press(act("save"));
    await waitFor("the no-library refusal", function () { return /at least one library/.test(toastText()); });
    ok("L6 no library ticked: refused on the page, the form left open", !!$("sf-type"), "form closed");
    await checkpoint("none");

    step = "all-ticked";
    noah[0].click();
    noah[1].click();
    press(act("save"));
    await waitFor("Noah's created toast", function () { return /Account created on Audiobookshelf/.test(toastText()); });
    ok("L7 all ticked: made", true, "");
    await checkpoint("all");
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
