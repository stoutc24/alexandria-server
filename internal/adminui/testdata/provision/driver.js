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
// library ticked, then made with all of them ticked: every library. Then
// "Change access" on Emily's login reads her account, changes it, and
// warns when the service changed more than asked; and "Test accounts" runs.
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

    // The address (owner, 2026-10-17): the Server Admin sign-in's first,
    // or another one typed.
    step = "address";
    await waitFor("the Server Admin address offered", function () { return visible($("sf-where")); });
    ok("W1 the Server Admin address comes first, chosen, the box hidden",
       $("sf-where-url").textContent === CFG.absUrl && $("sf-where-kept").checked && !visible($("sf-url")),
       $("sf-where-url").textContent + " kept=" + $("sf-where-kept").checked + " box=" + visible($("sf-url")));
    $("sf-where-new").click();
    ok("W2 Another address opens the box, empty", visible($("sf-url")) && $("sf-url").value === "", $("sf-url").value);
    $("sf-url").value = "http://192.0.2.7:13378";
    $("sf-url").dispatchEvent(new Event("input", { bubbles: true }));
    ok("W3 another server's address with the tick on: the page says where the account is made",
       visible($("sf-where-warn")) && $("sf-where-warn").textContent.indexOf(CFG.absHost) >= 0,
       $("sf-where-warn").textContent);
    $("sf-url").value = CFG.absUrl + "/";
    $("sf-url").dispatchEvent(new Event("input", { bubbles: true }));
    ok("W4 the same address typed: no warning", !visible($("sf-where-warn")), $("sf-where-warn").textContent);
    $("sf-where-kept").click();
    ok("W5 back to the Server Admin address: the box hidden again",
       !visible($("sf-url")) && !visible($("sf-where-warn")), "box=" + visible($("sf-url")));
    $("sf-where-new").click();
    $("sf-type").value = "jellyfin";
    $("sf-type").dispatchEvent(new Event("change", { bubbles: true }));
    ok("W6 a service with nothing kept: the box alone", !visible($("sf-where")) && visible($("sf-url")),
       "choice=" + visible($("sf-where")));
    $("sf-type").value = "abs";
    $("sf-type").dispatchEvent(new Event("change", { bubbles: true }));
    ok("W7 another service picked: its Server Admin address is chosen again",
       visible($("sf-where")) && $("sf-where-kept").checked && !visible($("sf-url")), "kept=" + $("sf-where-kept").checked);
    // The box holds a typed address: the kept one is still what is saved.
    $("sf-url").value = "http://192.0.2.7:13378";
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
    ok("W8 an edit shows the saved address in the box, no choice",
       !visible($("sf-where")) && visible($("sf-url")) && $("sf-url").value === CFG.absUrl, $("sf-url").value);
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

    // "Change access" on Emily's saved ABS login: her account read with the
    // kept sign-in, changed, and read back.
    step = "access";
    var change = await waitFor("Change access on her ABS login", function () {
      return document.querySelector('#acct-services [data-act="svc-access"]');
    });
    ok("A1 Change access is offered on a password login", /Change access/.test(change.textContent), change.textContent);
    var sid = change.getAttribute("data-sid");
    function panel() { return document.querySelector('#acct-services [data-access="' + sid + '"]'); }
    function inPanel(sel) { var p = panel(); return p ? p.querySelectorAll(sel) : []; }
    press(change);
    await waitFor("her access", function () { var p = panel(); return p && p.querySelector("h4"); });
    var radios = inPanel('input[type="radio"]'), libs = inPanel('input[name="svc-acc-lib"]'),
        dl = inPanel('input[name="svc-acc-dl"]')[0];
    ok("A2 it reads what she has: only Audiobooks, and downloads",
       radios.length === 2 && radios[1].checked && libs.length === 2 && libs[0].checked && !libs[1].checked &&
       dl && dl.checked, panel().textContent);
    ok("A3 a library's name is text, never markup",
       panel().textContent.indexOf("Podcasts <b>") >= 0 && !panel().querySelector("b"), panel().innerHTML);
    ok("A4 it says whose account, on which server", panel().textContent.indexOf("emily on " + CFG.absHost) >= 0 &&
       !/Their login uses/.test(panel().textContent), panel().textContent);
    ok("A5 focus moved into it", panel().contains(document.activeElement), document.activeElement && document.activeElement.outerHTML);

    libs[1].click();
    dl.click();
    press(panel().querySelector('[data-act="svc-access-save"]'));
    var saved = await waitFor("the saved line", function () { var p = panel(); return p && p.querySelector(".good"); });
    ok("A6 saved, saying what she has now", saved.textContent === "Saved. emily sees 2 of 2 libraries and can't download.",
       saved.textContent);
    await checkpoint("access");

    // Every library: the boxes follow the choice, and the save says so.
    radios = inPanel('input[type="radio"]');
    radios[0].click();
    libs = inPanel('input[name="svc-acc-lib"]');
    ok("A7 All libraries ticks and locks every box", libs[0].disabled && libs[1].disabled && libs[0].checked &&
       libs[1].checked, libs.length);
    press(panel().querySelector('[data-act="svc-access-save"]'));
    await waitFor("the second save", function () {
      var g = panel() && panel().querySelector(".good");
      return g && /every library/.test(g.textContent) ? g : null;
    });
    await checkpoint("access-all");

    // None ticked under "Only these" is refused on the page.
    inPanel('input[type="radio"]')[1].click();
    libs = inPanel('input[name="svc-acc-lib"]');
    libs[0].click();
    libs[1].click();
    press(panel().querySelector('[data-act="svc-access-save"]'));
    await waitFor("the refusal", function () {
      var b = panel() && panel().querySelector(".bad");
      return b && /at least one library/.test(b.textContent) ? b : null;
    });
    ok("A8 no library ticked: refused before anything is sent", true, "");
    press(panel().querySelector('[data-act="svc-access-close"]'));
    ok("A9 Close takes the panel away and focus back to the button", !panel() &&
       document.activeElement === document.querySelector('#acct-services [data-act="svc-access"][data-sid="' + sid + '"]'),
       document.activeElement && document.activeElement.outerHTML);
    await checkpoint("access-none");

    // run.sh has made ABS change a right nobody asked about: the change
    // shows as made, with a warning naming what else moved.
    step = "access-sneak";
    press(document.querySelector('#acct-services [data-act="svc-access"][data-sid="' + sid + '"]'));
    await waitFor("her access again", function () { var p = panel(); return p && p.querySelector("h4"); });
    inPanel('input[name="svc-acc-dl"]')[0].click();
    press(panel().querySelector('[data-act="svc-access-save"]'));
    var warn = await waitFor("the warning", function () { var p = panel(); return p && p.querySelector(".bad"); });
    ok("A10 a service that changed more than asked is named, not reported done",
       /also changed permissions\.update/.test(warn.textContent) && !panel().querySelector(".good"), warn.textContent);
    ok("A11 the panel shows what the account has now",
       inPanel('input[name="svc-acc-dl"]')[0].checked && inPanel('input[type="radio"]')[0].checked, panel().innerHTML);
    press(panel().querySelector('[data-act="svc-access-close"]'));
    await checkpoint("access-sneak");

    // "Test accounts" on the kept ABS sign-in: an old test account deleted,
    // a new one made, signed in as and deleted.
    step = "selftest";
    press(document.querySelector('#areas [data-area="admin"]'));
    var test = await waitFor("the Test accounts button", function () {
      return document.querySelector('#svc-admins [data-selftest="abs"]');
    });
    press(test);
    var result = await waitFor("the test's result", function () {
      var r = document.querySelector("#svc-admins .selftest strong");
      return r ? r.parentNode : null;
    }, 30000);
    ok("T1 the test passes on ABS", /New accounts work here/.test(result.textContent), result.textContent);
    ok("T2 five steps, all ticked, Change its access among them", result.querySelectorAll("li.ok").length === 5 &&
       !result.querySelector("li.bad") && /Change its access/.test(result.textContent), result.textContent);
    ok("T3 the old test account is named as cleared",
       /alexandria-selftest-old111/.test(result.textContent), result.textContent);
    await checkpoint("selftest");
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
