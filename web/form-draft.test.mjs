import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("./static/app.js", import.meta.url), "utf8");
function between(start, end) {
  const from = source.indexOf(start), to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from);
  return source.slice(from, to);
}
function harness() {
  const controls = [{ name: "engine", value: "engine-a" }, { name: "prompt", type: "textarea", value: "A retained prompt" }, { name: "seed", type: "number", value: "42" }, { name: "enabled", type: "checkbox", value: "on", checked: false }, { name: "source_image", type: "file", value: "not-restorable" }];
  const groups = [{ dataset: { parameterGroup: "Advanced" }, open: true }, { dataset: { parameterGroup: "Expert" }, open: false }];
  const context = {
    renderedEngine: "engine-a", engineDrafts: new Map(),
    el: { fields: { querySelectorAll: selector => selector === "[name]" ? controls : selector.endsWith("[open]") ? groups.filter(group => group.open) : groups }, error: {} },
    state: { uploads: 0, pendingInputs: { source_image: "old-token" }, inputRevisions: { source_image: "1" } },
    renderEngineFields: () => {}, setError: () => {}, say: () => {},
  };
  vm.createContext(context);
  vm.runInContext(between("function rememberEngineDraft()", "const el ="), context);
  vm.runInContext(source.split("\n").find(line => line.startsWith("function nextInputRevision(")), context);
  vm.runInContext(source.split("\n").find(line => line.startsWith("function resetGenerationForm()")), context);
  return { context, controls, groups };
}

test("engine reconstruction retains values, unchecked choices, and open disclosures without storing file paths", () => {
  const { context, controls, groups } = harness();
  context.rememberEngineDraft();
  assert.equal(context.engineDrafts.get("engine-a").values.source_image, undefined);
  controls[1].value = "default"; controls[2].value = ""; controls[3].checked = true;
  groups[0].open = false; groups[1].open = true;
  context.restoreEngineDraft({ id: "engine-a" });
  assert.equal(controls[1].value, "A retained prompt");
  assert.equal(controls[2].value, "42");
  assert.equal(controls[3].checked, false);
  assert.equal(groups[0].open, true);
  assert.equal(groups[1].open, false);
});

test("engine drafts do not leak to another engine", () => {
  const { context, controls } = harness();
  context.rememberEngineDraft(); controls[1].value = "engine-b defaults";
  context.restoreEngineDraft({ id: "engine-b" });
  assert.equal(controls[1].value, "engine-b defaults");
});

test("Reset invalidates the selected draft before reconstruction and preserves other engines", () => {
  const { context } = harness();
  context.rememberEngineDraft(); context.engineDrafts.set("engine-b", { values: { prompt: "other draft" } });
  let rebuilt = false;
  context.renderEngineFields = () => {
    assert.equal(context.renderedEngine, "");
    assert.equal(context.engineDrafts.has("engine-a"), false);
    assert.equal(Object.keys(context.state.pendingInputs).length, 0);
    assert.equal(context.state.inputRevisions.source_image, "2");
    rebuilt = true;
  };
  context.resetGenerationForm();
  assert.equal(rebuilt, true);
  assert.equal(context.engineDrafts.has("engine-b"), true);
});

test("Reset cannot discard input state while an upload is pending", () => {
  const { context } = harness(); context.rememberEngineDraft(); context.state.uploads = 1;
  context.renderEngineFields = () => assert.fail("must not reconstruct while uploading");
  context.resetGenerationForm();
  assert.equal(context.engineDrafts.has("engine-a"), true);
  assert.equal(context.state.pendingInputs.source_image, "old-token");
});
