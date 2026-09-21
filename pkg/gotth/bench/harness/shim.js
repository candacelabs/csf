/*
 * gotth-live comparative benchmark — the shared measurement shim.
 *
 * ONE FILE, BYTE-IDENTICAL, SERVED BY BOTH STACKS (equivalence-spec §2.0).
 * Its transfer bytes are subtracted from both stacks' client-JS figures with
 * the subtracted amount stated (§3.5). Do not fork it, do not branch inside it
 * on which stack loaded it, and do not let either application import from it —
 * a per-stack branch in the harness is a review finding (§3).
 *
 * It implements, verbatim, three definitions the spec makes executable:
 *
 *   §3.2  t_input  — the browser's own hardware timestamp for the native
 *                    pointerdown/keydown, captured by a {capture, passive}
 *                    listener registered at window BEFORE any application
 *                    script, so neither stack gains from whose listener runs
 *                    first.
 *   §3.1  paint_main — the performance.now() sampled on the first macrotask
 *                    after the requestAnimationFrame callback of the frame in
 *                    which the paint predicate first became true. MessageChannel
 *                    rather than setTimeout(...,0), to dodge the nested-timeout
 *                    4 ms clamp.
 *   §3.3  t_ready  — stamped by this file at the instant the application
 *                    assigns window.__bench.ready = true, so the stamp is taken
 *                    by the same code on both sides even though the condition
 *                    that triggers it is necessarily per-stack.
 *
 * Loaded as a classic script in <head>, before anything else. On the Next.js
 * side that is next/script with strategy="beforeInteractive"; on the
 * gotth-live side it is a plain <script src> ahead of the runtime's own tag.
 */
(function () {
  'use strict';

  if (window.__bench) {
    return;
  }

  var bench = {
    /* Bumped when the contract below changes in a way a harness must notice. */
    version: 1,

    /* §3.2 — most recent native input, and its browser-supplied timestamp. */
    t_input: null,
    lastInput: null,

    /* §3.3 — stamped by the setter installed below. */
    t_ready: null,

    /* Free-form annotations the app or the harness can leave behind. */
    marks: [],

    /* Every awaitPaint() resolution, in order, for post-hoc inspection. */
    samples: []
  };

  /* ---------------------------------------------------------------- §3.2 */

  function captureInput(ev) {
    /*
     * event.timeStamp is a DOMHighResTimeStamp on the same origin as
     * performance.now(), so latency = t_paint - t_input needs no clock
     * translation and no cross-process exchange. That is the entire reason
     * the spec puts the boundary inside the page.
     */
    bench.t_input = ev.timeStamp;
    bench.lastInput = {
      type: ev.type,
      key: typeof ev.key === 'string' ? ev.key : null,
      timeStamp: ev.timeStamp,
      target: describe(ev.target)
    };
  }

  function describe(node) {
    if (!node || node.nodeType !== 1) {
      return null;
    }
    var id = node.getAttribute('data-bench-id');
    return id ? '[data-bench-id=' + id + ']' : node.tagName.toLowerCase();
  }

  window.addEventListener('pointerdown', captureInput, { capture: true, passive: true });
  window.addEventListener('keydown', captureInput, { capture: true, passive: true });

  /* ---------------------------------------------------------------- §3.1 */

  /*
   * awaitPaint(region, predicate, options) -> Promise<sample>
   *
   *   region     "A".."E", the data-bench-region the interaction repaints, or
   *              an Element. The observer is scoped to that subtree because
   *              the ROI for the paint_present cross-check is that element's
   *              bounding box, and the two signals must be about the same
   *              pixels.
   *   predicate  () => boolean, evaluated on every observer callback. The
   *              harness supplies it from the interaction file, so the
   *              predicate text lives with the interaction ID, not here.
   *
   * Resolves with { t_input, t_paint, latency, mutations, predicateEvals }.
   */
  bench.awaitPaint = function awaitPaint(region, predicate, options) {
    var opts = options || {};
    var timeoutMs = typeof opts.timeoutMs === 'number' ? opts.timeoutMs : 10000;
    var tInput = typeof opts.tInput === 'number' ? opts.tInput : null;

    return new Promise(function (resolve, reject) {
      var root = resolveRegion(region);
      if (!root) {
        reject(new Error('bench: no region ' + JSON.stringify(region)));
        return;
      }

      var settled = false;
      var mutations = 0;
      var predicateEvals = 0;
      var channel = new MessageChannel();
      var timer = setTimeout(function () {
        finish(null, new Error('bench: paint predicate timed out after ' + timeoutMs + ' ms'));
      }, timeoutMs);

      var observer = new MutationObserver(function (records) {
        mutations += records.length;
        if (settled) {
          return;
        }
        predicateEvals++;
        var ok = false;
        try {
          ok = !!predicate();
        } catch (err) {
          finish(null, err);
          return;
        }
        if (!ok) {
          return;
        }
        /*
         * First true evaluation. Schedule the rAF now; its callback runs
         * immediately before the frame's rendering steps, and the message
         * posted from inside it is delivered as the first macrotask after
         * those steps have run. That macrotask's performance.now() is
         * paint_main.
         */
        settled = true;
        requestAnimationFrame(function () {
          channel.port2.postMessage(0);
        });
      });

      channel.port1.onmessage = function () {
        var tPaint = performance.now();
        var start = tInput !== null ? tInput : bench.t_input;
        finish({
          t_input: start,
          t_paint: tPaint,
          latency: start === null ? null : tPaint - start,
          mutations: mutations,
          predicateEvals: predicateEvals
        }, null);
      };

      function finish(sample, err) {
        clearTimeout(timer);
        observer.disconnect();
        channel.port1.onmessage = null;
        try {
          channel.port1.close();
          channel.port2.close();
        } catch (ignored) {
          /* Older engines lack MessagePort.close(); the ports are garbage
             anyway once both ends are unreachable. */
        }
        if (err) {
          reject(err);
          return;
        }
        bench.samples.push(sample);
        resolve(sample);
      }

      observer.observe(root, {
        subtree: true,
        childList: true,
        characterData: true,
        attributes: true
      });

      /*
       * The predicate may already hold — a push can land between the
       * harness deciding to observe and the observer attaching. Evaluating
       * once here would report a paint that happened before we looked, which
       * is worse than missing it, so we deliberately do NOT: the harness
       * calls awaitPaint() before it dispatches the input.
       */
    });
  };

  function resolveRegion(region) {
    if (region && region.nodeType === 1) {
      return region;
    }
    if (region === undefined || region === null) {
      return document.documentElement;
    }
    return document.querySelector('[data-bench-region="' + String(region) + '"]');
  }

  /* Region-of-interest for the paint_present cross-check (§3.1). */
  bench.regionRect = function regionRect(region) {
    var root = resolveRegion(region);
    if (!root) {
      return null;
    }
    var r = root.getBoundingClientRect();
    return { x: r.x, y: r.y, width: r.width, height: r.height };
  };

  /* textContent of the value carrier, for predicates and assertions. */
  bench.value = function value(id) {
    var el = document.querySelector('[data-bench-id="' + String(id) + '"]');
    return el ? el.textContent : null;
  };

  bench.mark = function mark(name, detail) {
    bench.marks.push({ name: name, detail: detail === undefined ? null : detail, t: performance.now() });
  };

  /* ---------------------------------------------------------------- §3.3 */

  var readyValue = false;
  var readyWaiters = [];

  Object.defineProperty(bench, 'ready', {
    enumerable: true,
    get: function () {
      return readyValue;
    },
    set: function (next) {
      var truthy = !!next;
      /*
       * "exactly once" (§3.3). A second assignment does not move the stamp;
       * it is recorded as a mark so a double-signal is visible in the data
       * rather than silently overwriting the number D5 publishes.
       */
      if (truthy && readyValue) {
        bench.mark('ready:duplicate');
        return;
      }
      readyValue = truthy;
      if (!truthy) {
        return;
      }
      bench.t_ready = performance.now();
      var waiters = readyWaiters;
      readyWaiters = [];
      for (var i = 0; i < waiters.length; i++) {
        waiters[i](bench.t_ready);
      }
    }
  });

  bench.whenReady = function whenReady() {
    if (readyValue) {
      return Promise.resolve(bench.t_ready);
    }
    return new Promise(function (resolve) {
      readyWaiters.push(resolve);
    });
  };

  window.__bench = bench;
})();
