import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("./static/app.js", import.meta.url), "utf8");

function sourceLine(prefix) {
  const line = source.split("\n").find((candidate) => candidate.startsWith(prefix));
  assert.ok(line, `missing ${prefix} in app.js`);
  return line;
}

function makeHarness(hash) {
  const calls = {
    stopInstallerPolling: 0,
    loadJobs: 0,
    loadModels: 0,
    loadInstaller: 0,
    loadSystem: 0,
  };
  const views = ["generate", "history", "models", "system"].map((id) => ({ id, hidden: id !== "generate" }));
  const links = views.map(({ id }) => ({
    dataset: { viewLink: id },
    attributes: new Map(),
    setAttribute(name, value) {
      this.attributes.set(name, value);
    },
    removeAttribute(name) {
      this.attributes.delete(name);
    },
  }));
  const titles = Object.fromEntries(views.map(({ id }) => [id, {
    id: `${id}-title`,
    focused: false,
    focus() {
      this.focused = true;
    },
  }]));
  const main = {
    id: "main-content",
    focused: false,
    focus() {
      this.focused = true;
    },
  };
  const document = {
    querySelectorAll(selector) {
      if (selector === ".view") return views;
      if (selector === "[data-view-link]") return links;
      return [];
    },
    querySelector(selector) {
      if (selector === "#main-content") return main;
      if (selector.startsWith("#") && selector.endsWith("-title")) {
        return titles[selector.slice(1, -6)] || null;
      }
      return null;
    },
  };
  const context = {
    document,
    location: { hash },
    queueMicrotask: (callback) => callback(),
    stopInstallerPolling: () => { calls.stopInstallerPolling += 1; },
    loadJobs: () => { calls.loadJobs += 1; },
    loadModels: () => { calls.loadModels += 1; },
    loadInstaller: () => { calls.loadInstaller += 1; },
    loadSystem: () => { calls.loadSystem += 1; },
  };
  return { calls, context, links, main, route: null, focusMainFromSkip: null, views };
}

function loadNavigationFunctions(harness) {
  harness.route = vm.runInNewContext(`(${sourceLine("function route()")})`, harness.context);
  harness.focusMainFromSkip = vm.runInNewContext(
    `(${sourceLine("function focusMainFromSkip")})`,
    harness.context,
  );
}

function resetCalls(calls) {
  Object.keys(calls).forEach((key) => {
    calls[key] = 0;
  });
}

function activateSkipLink(harness) {
  const event = {
    defaultPrevented: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
  };
  harness.main.focused = false;
  harness.focusMainFromSkip(event);
  return event;
}

test("skip link preserves the models route and focuses main", () => {
  const harness = makeHarness("#models");
  loadNavigationFunctions(harness);

  harness.route();
  assert.equal(harness.views.find(({ id }) => id === "models").hidden, false);
  assert.equal(harness.calls.loadModels, 1);
  assert.equal(harness.calls.loadInstaller, 1);

  resetCalls(harness.calls);
  const event = activateSkipLink(harness);

  assert.equal(event.defaultPrevented, true);
  assert.equal(harness.context.location.hash, "#models");
  assert.equal(harness.main.focused, true);
  assert.equal(harness.views.find(({ id }) => id === "models").hidden, false);
  assert.deepEqual(harness.calls, {
    stopInstallerPolling: 0,
    loadJobs: 0,
    loadModels: 0,
    loadInstaller: 0,
    loadSystem: 0,
  });
});

test("skip link preserves the system route and does not restart diagnostics", () => {
  const harness = makeHarness("#system");
  loadNavigationFunctions(harness);

  harness.route();
  assert.equal(harness.views.find(({ id }) => id === "system").hidden, false);
  assert.equal(harness.calls.stopInstallerPolling, 1);
  assert.equal(harness.calls.loadSystem, 1);

  resetCalls(harness.calls);
  const event = activateSkipLink(harness);

  assert.equal(event.defaultPrevented, true);
  assert.equal(harness.context.location.hash, "#system");
  assert.equal(harness.main.focused, true);
  assert.equal(harness.views.find(({ id }) => id === "system").hidden, false);
  assert.deepEqual(harness.calls, {
    stopInstallerPolling: 0,
    loadJobs: 0,
    loadModels: 0,
    loadInstaller: 0,
    loadSystem: 0,
  });
});
