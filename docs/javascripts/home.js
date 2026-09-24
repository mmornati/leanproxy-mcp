/* Landing page: the draggable scanner, its live readout, and copy buttons. */
(function () {
  "use strict";

  function init() {
    var home = document.querySelector(".lp-home");
    if (!home || home.dataset.bound) return;
    home.dataset.bound = "1";

    var fig = home.querySelector("[data-scanner]");
    var range = fig && fig.querySelector(".lp-scanner__range");
    var weightOut = fig && fig.querySelector("[data-weight]");
    var threatOut = fig && fig.querySelector("[data-threats]");
    var VIEW_W = 1200;
    var ROUTER = 318;

    var items = [];
    if (fig) {
      fig.querySelectorAll(".xr-before .xr-item").forEach(function (g) {
        items.push({ x: +g.dataset.x, tok: +g.dataset.tok });
      });
    }
    var KEY_X = 520;

    function fmt(n) {
      return n.toLocaleString("en-US");
    }

    function setScan(pct) {
      if (!fig) return;
      fig.style.setProperty("--scan", pct + "%");
      fig.style.setProperty("--belt", pct * 4 + "px");
      var x = (pct / 100) * VIEW_W;
      var remaining = 0;
      var scanned = 0;
      items.forEach(function (it) {
        if (it.x > x) remaining += it.tok;
        else scanned++;
      });
      var weight = remaining + (scanned > 0 ? ROUTER : 0);
      if (weightOut) {
        weightOut.textContent = fmt(weight);
        weightOut.dataset.state = remaining === 0 ? "lean" : "";
      }
      if (threatOut) {
        var boxed = x >= KEY_X;
        threatOut.textContent = boxed ? "1 boxed" : "0 boxed";
        threatOut.dataset.state = boxed ? "boxed" : "";
      }
      if (range && +range.value !== pct) range.value = pct;
    }

    if (range) {
      range.addEventListener("input", function () {
        cancelSweep();
        setScan(+range.value);
      });
    }

    /* One authored motion: the first sweep, from the belt's entry to rest. */
    var raf = 0;
    function cancelSweep() {
      if (raf) cancelAnimationFrame(raf);
      raf = 0;
    }
    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    var REST = 62;
    if (reduce || !fig) {
      setScan(REST);
    } else {
      setScan(0);
      var start = 0;
      var DUR = 2600;
      var sweep = function (t) {
        if (!start) start = t;
        var p = Math.min(1, (t - start) / DUR);
        var e = 1 - Math.pow(2, -10 * p); /* exponential ease-out */
        setScan(Math.round((2 + (100 - 2) * e) * 10) / 10);
        if (p < 1) raf = requestAnimationFrame(sweep);
        else settle();
      };
      var settle = function () {
        var from = 100;
        var s0 = 0;
        var back = function (t) {
          if (!s0) s0 = t;
          var p = Math.min(1, (t - s0) / 900);
          var e = 1 - Math.pow(1 - p, 3);
          setScan(Math.round((from + (REST - from) * e) * 10) / 10);
          if (p < 1) raf = requestAnimationFrame(back);
          else raf = 0;
        };
        raf = requestAnimationFrame(back);
      };
      var io = new IntersectionObserver(function (entries) {
        if (entries[0].isIntersecting) {
          io.disconnect();
          setTimeout(function () {
            raf = requestAnimationFrame(sweep);
          }, 350);
        }
      }, { threshold: 0.4 });
      io.observe(fig);
    }

    /* Measurement strips grow once when they scroll in. */
    var strips = home.querySelector(".lp-strips");
    if (strips && !reduce && "IntersectionObserver" in window) {
      home.classList.add("is-ready");
      var io2 = new IntersectionObserver(function (entries) {
        if (entries[0].isIntersecting) {
          strips.classList.add("is-in");
          io2.disconnect();
        }
      }, { threshold: 0.3 });
      io2.observe(strips);
    }

    /* Copy buttons */
    home.querySelectorAll(".lp-copy").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var label = btn.querySelector(".lp-copy__label");
        var done = function (ok) {
          btn.dataset.state = ok ? "done" : "";
          if (label) label.textContent = ok ? "Copied" : "Copy failed";
          setTimeout(function () {
            btn.dataset.state = "";
            if (label) label.textContent = "Copy";
          }, 1800);
        };
        if (navigator.clipboard) {
          navigator.clipboard.writeText(btn.dataset.copy).then(
            function () { done(true); },
            function () { done(false); }
          );
        } else {
          done(false);
        }
      });
    });
  }

  if (window.document$ && typeof window.document$.subscribe === "function") {
    window.document$.subscribe(init);
  } else if (document.readyState !== "loading") {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init);
  }
})();
