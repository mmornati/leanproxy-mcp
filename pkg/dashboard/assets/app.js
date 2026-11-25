// LeanProxy admin: the two things htmx does not do by itself.
(function () {
  "use strict";

  // 1. A polled fragment replaces its <details> elements, which would
  //    close whatever the reader had opened. Remember the open ones before
  //    each swap and reopen them after.
  var open = {};
  document.addEventListener("htmx:beforeSwap", function (ev) {
    var target = ev.detail.target;
    if (!target || !target.id) return;
    open[target.id] = Array.prototype.map.call(
      target.querySelectorAll("details[open][id]"),
      function (d) { return d.id; }
    );
  });
  document.addEventListener("htmx:afterSwap", function (ev) {
    var target = ev.detail.target;
    if (!target || !target.id || !open[target.id]) return;
    open[target.id].forEach(function (id) {
      var d = document.getElementById(id);
      if (d) d.open = true;
    });
  });

  // 2. The add-server form shows the fields of the chosen transport.
  function syncTransport(select) {
    var form = select.form;
    if (!form) return;
    form.querySelectorAll("[data-for-transport]").forEach(function (el) {
      var on = el.getAttribute("data-for-transport").split(" ").indexOf(select.value) !== -1;
      el.hidden = !on;
      el.querySelectorAll("input, textarea").forEach(function (f) { f.disabled = !on; });
    });
  }
  document.addEventListener("change", function (ev) {
    if (ev.target.matches && ev.target.matches("[data-transport-select]")) syncTransport(ev.target);
  });
  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("[data-transport-select]").forEach(syncTransport);
  });

  // A successful add clears the form.
  document.addEventListener("htmx:afterRequest", function (ev) {
    var el = ev.detail.elt;
    if (el && el.tagName === "FORM" && ev.detail.successful && !document.querySelector("#flash .bad")) {
      el.reset();
      el.querySelectorAll("[data-transport-select]").forEach(syncTransport);
    }
  });
})();
