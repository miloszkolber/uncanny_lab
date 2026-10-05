import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("./static/app.js", import.meta.url), "utf8");
function load(context, ...names) {
  vm.createContext(context);
  for (const name of names) {
    const match = new RegExp(`^(?:async )?function ${name}\\(`, "m").exec(source);
    assert.ok(match, `missing ${name}`);
    const rest = source.slice(match.index);
    const end = rest.search(/\n(?=(?:async )?function |const |el\.|document\.|window\.)/);
    vm.runInContext(end < 0 ? rest : rest.slice(0, end), context);
  }
  return context;
}
function deferred() { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
function control(action) {
  return { disabled: false, isConnected: true, dataset: { jobAction: action, unavailable: "false" }, attributes: new Map(), setAttribute(name, value) { this.attributes.set(name, value); }, removeAttribute(name) { this.attributes.delete(name); }, closest() { return this.card; } };
}
function card(id, action) {
  const button = control(action), errors = [];
  const node = { dataset: { jobId: id }, querySelectorAll(selector) { return selector === "[data-job-action]" ? [button] : errors; }, querySelector() { return { append(error) { errors.push(error); } }; }, button, errors };
  button.card = node;
  return node;
}
function actionHarness(action) {
  const original = card("fixture-job", action), live = [original], calls = [], result = deferred();
  const context = {
    state: { historyActions: new Map(), selectedEngine: { "image-to-image": "neural-style" }, inputRevisions: {}, pendingInputs: {} },
    el: { history: { querySelectorAll: () => live }, jobError: {} },
    document: { createElement: () => ({ dataset: {}, setAttribute() {} }) },
    api: (path, options) => { calls.push({ path, options }); return result.promise; },
    defaultImageEngine: () => "activation-max", renderInputToken: (kind, token) => { context.state.pendingInputs[kind] = token; }, selectMode: mode => { context.state.mode = mode; },
    renderCurrent: job => { context.state.currentJob = job; }, scheduleJobRefresh() {}, location: { hash: "#history" }, say() {},
  };
  load(context, "setError", "busy", "nextInputRevision", "applyHistoryActionState", "syncHistoryActionState", "jobAction", "duplicate", "useAsInput");
  return { context, original, live, calls, result };
}

test("job parameters preserve the API JSON object, false/zero and nested values; legacy strings remain bounded to objects", () => {
  const context = load({}, "jobParameters", "jobPrompt");
  const parameters = { prompt: "Complete API prompt", enabled: false, iterations: 0, nested: { weight: 0.05 } };
  assert.equal(context.jobParameters({ parameters }), parameters);
  assert.equal(context.jobPrompt({ parameters }), parameters.prompt);
  assert.equal(context.jobParameters({ parameters: JSON.stringify(parameters) }).nested.weight, 0.05);
  for (const parameters of [undefined, null, [], "[]", "null", "false", "7", "broken", 7, false]) assert.equal(Object.keys(context.jobParameters({ parameters })).length, 0);
});

for (const action of ["duplicate", "input"]) test(`${action} keeps pending/error state by job/action across replaced cards, deduplicates and retries`, async () => {
  const f = actionHarness(action), job = { id: "fixture-job" };
  const invoke = button => action === "duplicate" ? f.context.duplicate(job, button) : f.context.useAsInput(job, "", button);
  const pending = invoke(f.original.button);
  assert.equal(f.original.button.disabled, true);
  f.original.button.isConnected = false;
  const successor = card(job.id, action), other = card("other-job", action);
  f.live.splice(0, 1, successor, other);
  f.context.applyHistoryActionState(successor);
  assert.equal(successor.button.disabled, true);
  assert.equal(successor.button.attributes.get("aria-busy"), "true");
  await invoke(successor.button);
  assert.equal(f.calls.length, 1);
  f.result.reject(new Error(`${action} unavailable. Try again.`)); await pending;
  assert.equal(successor.button.disabled, false);
  assert.equal(successor.button.attributes.has("aria-busy"), false);
  assert.equal(successor.errors[0].textContent, `${action} unavailable. Try again.`);
  assert.equal(other.errors.length, 0);
  const refreshed = card(job.id, action); f.live.splice(0, 1, refreshed); f.context.applyHistoryActionState(refreshed);
  assert.equal(refreshed.errors[0].textContent, successor.errors[0].textContent);
  f.context.api = async path => { f.calls.push({ path }); throw new Error("Retry unavailable on the same job"); };
  await invoke(refreshed.button);
  assert.equal(f.calls.length, 2); assert.ok(f.calls.every(call => call.path.includes(job.id)));
  assert.equal(refreshed.errors[0].textContent, "Retry unavailable on the same job");
  assert.equal(refreshed.button.disabled, false);
});

for (const newerUpload of [false, true]) test(`Reset invalidates late input selection${newerUpload ? " and never reuses its revision for a newer upload" : " without pretending to cancel accepted work"}`, async () => {
  const f = actionHarness("input"), job = { id: "fixture-job" };
  Object.assign(f.context, { renderedEngine: "neural-style", engineDrafts: new Map(), renderEngineFields() {}, FormData: class { append() {} }, URL: { createObjectURL: () => "blob:fixture", revokeObjectURL() {} }, uploadInput: () => null, syncUploadConstraint() {} });
  Object.assign(f.context.state, { uploads: 0 }); Object.assign(f.context.el, { error: {}, submit: {} });
  load(f.context, "resetGenerationForm", "upload");
  const pending = f.context.useAsInput(job, "", f.original.button), originalRevision = f.context.state.inputRevisions.source_image;
  f.context.resetGenerationForm();
  const resetRevision = f.context.state.inputRevisions.source_image;
  assert.notEqual(resetRevision, originalRevision);
  if (newerUpload) {
    f.context.api = async () => ({ token: "inputs/newer-b.png" });
    await f.context.upload({}, "source_image", { dataset: {} });
    assert.notEqual(f.context.state.inputRevisions.source_image, originalRevision);
    assert.notEqual(f.context.state.inputRevisions.source_image, resetRevision);
  }
  f.result.resolve({ token: "inputs/older-a.png", template: { parameters: { source_image: "inputs/older-a.png" } } }); await pending;
  assert.equal(f.context.state.pendingInputs.source_image, newerUpload ? "inputs/newer-b.png" : undefined);
  assert.equal(f.context.state.selectedEngine["image-to-image"], "neural-style");
  assert.equal(f.context.location.hash, "#history");
  assert.equal(f.calls.length, 1, "Reset must not send a cancellation request");
});

function installerHarness() {
  const v1 = { version: "policy-v1", text: "Original permission terms" }, v2 = { version: "policy-v2", text: "Changed permission terms" }, posts = [];
  let focus;
  const context = {
    state: { installer: { data: { policy: v1 }, displayedPolicy: null, acceptedPolicy: null, policyStale: false, starting: false } },
    el: { installerPolicy: {}, installerAck: { checked: false }, installerConfirm: { setAttribute() {}, removeAttribute() {} }, installerError: {}, installerDialog: { open: false, scrollTop: 200, querySelector: () => ({ focus() { focus = "Close"; } }), close() { this.open = false; } } },
    text: (value, fallback) => value || fallback, say() {},
    api: async (_, options) => { posts.push(JSON.parse(options.body)); if (posts.length === 1) throw Object.assign(new Error("Reload the policy before installing"), { code: "STALE_POLICY" }); throw new Error("Fixture retry rejected; no installation"); },
    loadInstaller: async () => { context.state.installer.data = { policy: v2 }; context.state.installer.policyStale = false; context.syncInstallerDialogPolicy(v2); },
  };
  load(context, "setError", "sameInstallerPolicy", "syncInstallerConsent", "presentInstallerPolicy", "syncInstallerDialogPolicy", "acknowledgeInstallerPolicy", "openInstallerDialog", "startInstaller");
  return { context, v1, v2, posts, focused: () => focus };
}

test("stale policy replaces displayed terms, clears consent and cannot submit unshown/unacknowledged v2", async () => {
  const f = installerHarness(), c = f.context;
  c.openInstallerDialog(); c.el.installerDialog.open = true; c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy();
  await c.startInstaller();
  assert.equal(c.el.installerPolicy.textContent, f.v2.text);
  assert.equal(c.el.installerAck.checked, false); assert.equal(c.el.installerConfirm.disabled, true);
  assert.equal(c.el.installerDialog.scrollTop, 0); assert.equal(f.focused(), "Close");
  await c.startInstaller(); assert.equal(f.posts.length, 1);
  c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy(); await c.startInstaller();
  assert.deepEqual(f.posts, [{ accepted: true, policy_version: "policy-v1" }, { accepted: true, policy_version: "policy-v2" }]);
});

test("policy text changes under the same version also invalidate consent, while unchanged refresh preserves it", () => {
  const f = installerHarness(), c = f.context;
  c.openInstallerDialog(); c.el.installerDialog.open = true; c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy();
  c.syncInstallerDialogPolicy({ ...f.v1 }); assert.equal(c.el.installerConfirm.disabled, false);
  const changed = { ...f.v1, text: "Corrected terms" }; c.state.installer.data.policy = changed; c.syncInstallerDialogPolicy(changed);
  assert.equal(c.el.installerPolicy.textContent, changed.text); assert.equal(c.el.installerAck.checked, false); assert.equal(c.el.installerConfirm.disabled, true);
});

test("pending installation cannot be reenabled by acknowledgment changes; failed policy refresh stays gated", async () => {
  const f = installerHarness(), c = f.context, response = deferred(); c.api = () => response.promise;
  c.openInstallerDialog(); c.el.installerDialog.open = true; c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy(); const pending = c.startInstaller();
  c.el.installerAck.checked = false; c.acknowledgeInstallerPolicy(); c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy(); assert.equal(c.el.installerConfirm.disabled, true);
  c.loadInstaller = async () => {}; response.reject(Object.assign(new Error("Reload the policy before installing"), { code: "STALE_POLICY" })); await pending;
  c.el.installerAck.checked = true; c.acknowledgeInstallerPolicy(); assert.equal(c.el.installerConfirm.disabled, true); assert.equal(c.state.installer.policyStale, true);
});

test("initial list selects a running job before detail completion and late detail cannot replace a newer accepted job", async () => {
  const response = deferred(), current = { id: "initial-job", status: "running", parameters: { prompt: "API object" } }, newer = { id: "accepted-job", status: "queued" }, reads = [], renders = [];
  const context = { state: { currentJob: null }, el: { jobError: {} }, api: path => { reads.push(path); return response.promise; }, renderCurrent: job => { context.state.currentJob = job; renders.push(job); }, scheduleJobRefresh() {}, setError() {} };
  load(context, "refreshJob", "reconcileJobs");
  context.reconcileJobs([current], true);
  assert.equal(context.state.currentJob, current); assert.deepEqual(reads, ["/api/jobs/initial-job"]);
  context.renderCurrent(newer); response.resolve({ ...current, progress_step: 5 }); await response.promise; await Promise.resolve();
  assert.equal(context.state.currentJob, newer); assert.equal(renders.length, 2);
  context.reconcileJobs([current], true); assert.equal(context.state.currentJob, newer); assert.equal(reads.length, 1);
});

test("API errors retain the structured STALE_POLICY code at the consumer boundary", async () => {
  const context = { state: { mutations: {}, request: {}, controllers: {} }, AbortController, DOMException, fetch: async () => ({ ok: false, status: 409, json: async () => ({ error: { code: "STALE_POLICY", message: "Reload the policy before installing" } }) }) };
  load(context, "api");
  await assert.rejects(context.api("/api/model-installer/install", { method: "POST" }), error => error.code === "STALE_POLICY" && /Reload/.test(error.message));
});
