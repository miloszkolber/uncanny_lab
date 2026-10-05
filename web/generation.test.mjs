import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("./static/app.js", import.meta.url), "utf8");
const submitSource = source.split("\n").find(line => line.startsWith('el.form.addEventListener("submit"'));

function harness({ fields = {}, required = [], token = "", reject = false } = {}) {
  const attributes = new Map();
  const engine = { id: "fixture-engine", parameters: { prompt: { type: "string" }, enabled: { type: "boolean", default: true }, iterations: { type: "integer" } }, required_inputs: required };
  const data = new Map(Object.entries({ engine: engine.id, prompt: "A quiet orchard", iterations: "7", ...fields }));
  const calls = [], errors = [];
  let submit;
  const context = {
    el: { form: { addEventListener: (name, callback) => { submit = callback; } }, error: {}, submit: { disabled: false, setAttribute: (name, value) => attributes.set(name, value), removeAttribute: name => attributes.delete(name) } },
    state: { engines: [engine], mutations: {}, uploads: 0 },
    FormData: class { get(name) { return data.get(name) ?? null; } has(name) { return data.has(name); } },
    ready: () => true, inputPreview: () => ({ dataset: { token } }),
    setError: (_, message = "") => errors.push(message),
    api: async (url, options) => { calls.push({ url, options, body: JSON.parse(options.body) }); if (reject) throw new Error("Queue is unavailable. Try again."); return { id: "fixture-job", status: "queued" }; },
    renderCurrent: () => {}, loadJobs: () => {}, say: () => {},
  };
  vm.runInNewContext(submitSource, context);
  return { context, calls, errors, attributes, submit: () => submit({ preventDefault() {} }) };
}

test("generation submits an explicit false for unchecked booleans instead of restoring a true engine default", async () => {
  const fixture = harness();
  await fixture.submit();
  assert.equal(fixture.calls.length, 1);
  assert.deepEqual(fixture.calls[0].body, { engine: "fixture-engine", parameters: { prompt: "A quiet orchard", enabled: false, iterations: 7 } });
});

test("generation reports missing required upload tokens without queueing", async () => {
  const fixture = harness({ required: ["source_image"] });
  await fixture.submit();
  assert.equal(fixture.calls.length, 0);
  assert.equal(fixture.errors.at(-1), "source image is required.");
});

test("generation failure is reported and restores the submit control for retry", async () => {
  const fixture = harness({ reject: true });
  await fixture.submit();
  assert.equal(fixture.errors.at(-1), "Queue is unavailable. Try again.");
  assert.equal(fixture.context.el.submit.disabled, false);
  assert.equal(fixture.attributes.has("aria-busy"), false);
  await fixture.submit();
  assert.equal(fixture.calls.length, 2);
});

test("request ownership deduplicates pending mutations and releases the key on failure", async () => {
  let finish, count = 0;
  const context = {
    state: { mutations: {}, request: {}, controllers: {} }, AbortController, DOMException,
    fetch: () => { count += 1; return new Promise(resolve => { finish = resolve; }); },
  };
  vm.runInNewContext(source.split("\n").find(line => line.startsWith("function api(")), context);
  const first = context.api("/api/jobs", { method: "POST" }, "create");
  const second = context.api("/api/jobs", { method: "POST" }, "create");
  assert.equal(first, second);
  assert.equal(count, 1);
  finish({ ok: false, status: 503, json: async () => ({ error: { message: "Fixture unavailable" } }) });
  await assert.rejects(first, /Fixture unavailable/);
  assert.equal(context.state.mutations.create, undefined);
  assert.equal(context.state.controllers.create, undefined);
});

test("decimal parameter controls accept fractional defaults while integer controls retain whole-number steps", () => {
  const start = source.indexOf("function schemaControl("), end = source.indexOf("async function upload(", start);
  const context = { document: { createElement: () => ({}) }, labelField: (_, control) => control };
  vm.runInNewContext(source.slice(start, end), context);
  const decimal = context.schemaControl("learning_rate", { type: "number", default: 0.05, min: 0.00001, max: 1 });
  assert.equal(decimal.type, "number");
  assert.equal(decimal.step, "any");
  assert.equal(decimal.value, "0.05");
  const integer = context.schemaControl("iterations", { type: "integer", default: 300, min: 1 });
  assert.equal(integer.step, "1");
});

test("initial Models failure exposes recovery on Models, not only in the hidden generation form", async () => {
  const start = source.indexOf("async function init()"), end = source.indexOf('window.addEventListener("hashchange"', start);
  const errors = [], retries = [];
  let retried = false;
  const context = {
    state: { initAttempt: 0, jobs: [], request: {}, connection: {} },
    el: { models: {}, modelsError: {} }, location: { hash: "#models" },
    route: () => {}, syncCommandShortcutLabel: () => {}, loadJobs: () => {}, connectEventStream: () => {},
    api: (path, _, key) => { context.state.request[key] = 1; return path === "/api/engines" ? Promise.resolve([{ id: "fixture-engine" }]) : Promise.reject(new Error("Inventory temporarily unavailable")); },
    setModels: () => assert.fail("failed inventory must not be accepted"), renderEngineFields: () => {}, renderHistory: () => {}, refreshConnectionStatus: () => {}, showInitError: () => {},
    setError: (node, message) => errors.push({ node, message }), retryState: (node, message, retry) => retries.push({ node, message, retry }), loadModels: () => { retried = true; },
  };
  vm.runInNewContext(source.slice(start, end), context);
  await context.init();
  assert.equal(errors[0].node, context.el.modelsError);
  assert.match(errors[0].message, /Inventory temporarily unavailable/);
  assert.equal(retries[0].node, context.el.models);
  retries[0].retry(); assert.equal(retried, true);
});
