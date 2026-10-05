const $ = (s) => document.querySelector(s);
const state = { engines: [], modelStatuses: {}, jobs: [], historyFilter: "all", historyActions: new Map(), currentJob: null, jobStatusKey: "", jobHasMore: false, jobListDebounce: null, mode: "text-to-image", selectedEngine: {}, selectedPreview: null, previewPinned: false, pendingInputs: {}, inputRevisions: {}, uploads: 0, controllers: {}, request: {}, mutations: {}, deleteId: null, deletePending: false, jobPoll: null, stream: null, initAttempt: 0, timelineJob: "", detailZoom: 1, connection: { api: "pending", stream: "pending" }, installer: { data: null, poll: null, lastPhase: null, displayedPolicy: null, acceptedPolicy: null, policyStale: false, starting: false } };
let toastTimer;
let toastSequence = 0;
// Drafts belong to an engine, not to transient DOM rebuilt by model refreshes.
const engineDrafts = new Map();
let renderedEngine = "";
function rememberEngineDraft() {
  if (!renderedEngine) return;
  const values = {};
  el.fields.querySelectorAll("[name]").forEach((control) => {
    if (control.name === "engine" || control.type === "file") return;
    values[control.name] = { value: control.value, checked: control.checked };
  });
  const open = [...el.fields.querySelectorAll("details[data-parameter-group][open]")].map((group) => group.dataset.parameterGroup);
  engineDrafts.set(renderedEngine, { values, open });
}
function restoreEngineDraft(engine) {
  const draft = engineDrafts.get(engine.id);
  if (!draft) return;
  el.fields.querySelectorAll("[name]").forEach((control) => {
    const saved = draft.values[control.name];
    if (!saved || control.name === "engine" || control.type === "file") return;
    if (control.type === "checkbox") control.checked = saved.checked;
    else control.value = saved.value;
  });
  el.fields.querySelectorAll("details[data-parameter-group]").forEach((group) => { group.open = draft.open.includes(group.dataset.parameterGroup); });
}
const el = { form: $("#generate-form"), fields: $("#engine-fields"), engineStatus: $("#engine-status"), progressRow: $("#progress-row"), error: $("#form-error"), jobError: $("#job-error"), preview: $("#preview"), empty: $("#empty-preview"), progress: $("#progress"), progressLabel: $("#progress-label"), status: $("#job-status"), timeline: $("#timeline"), cancel: $("#cancel"), rerun: $("#rerun"), export: $("#export"), downloadFrame: $("#download-frame"), useFrame: $("#use-frame"), submit: $("#submit-job"), reset: $("#reset-form"), unpin: $("#unpin-preview"), history: $("#history-grid"), historyFilter: $("#history-status-filter"), historySummary: $("#history-summary"), models: $("#models-list"), modelsError: $("#models-error"), installer: $("#bundle-installer"), installerDialog: $("#installer-dialog"), installerPolicy: $("#installer-dialog-policy"), installerAck: $("#installer-acknowledgement"), installerConfirm: $("#installer-confirm"), installerError: $("#installer-dialog-error"), confirmError: $("#confirm-error"), confirmProgress: $("#confirm-progress"), system: $("#system-grid"), announce: $("#announcements"), detail: $("#detail-dialog"), detailImage: $("#detail-image"), detailEmpty: $("#detail-empty"), detailViewport: $("#detail-image-viewport"), zoomOut: $("#detail-zoom-out"), zoomReset: $("#detail-zoom-reset"), zoomIn: $("#detail-zoom-in"), zoomLevel: $("#detail-zoom-level"), confirm: $("#confirm-dialog") };
// Inline Mewa image-line, external-link-line, and arrow-down-s-line assets.
const ICON_PATHS = {
  image: '<path d="M2.9918 21C2.44405 21 2 20.5551 2 20.0066V3.9934C2 3.44476 2.45531 3 2.9918 3H21.0082C21.556 3 22 3.44495 22 3.9934V20.0066C22 20.5552 21.5447 21 21.0082 21H2.9918ZM20 15V5H4V19L14 9L20 15ZM20 17.8284L14 11.8284L6.82843 19H20V17.8284ZM8 11C6.89543 11 6 10.1046 6 9C6 7.89543 6.89543 7 8 7C9.10457 7 10 7.89543 10 9C10 10.1046 9.10457 11 8 11Z"/>',
  "external-link": '<path d="M10 6V8H5V19H16V14H18V20C18 20.5523 17.5523 21 17 21H4C3.44772 21 3 20.5523 3 20V7C3 6.44772 3.44772 6 4 6H10ZM21 3V11H19L18.9999 6.413L11.2071 14.2071L9.79289 12.7929L17.5849 5H13V3H21Z"/>',
  chevron: '<path d="M11.9999 13.1714L16.9497 8.22168L18.3639 9.63589L11.9999 15.9999L5.63599 9.63589L7.0502 8.22168L11.9999 13.1714Z"/>',
};
const icon = (name) => { const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg"); svg.setAttribute("aria-hidden", "true"); svg.setAttribute("viewBox", "0 0 24 24"); svg.setAttribute("fill", "currentColor"); svg.innerHTML = ICON_PATHS[name] || ""; return svg; };
function disclosureSummary(label) { const summary = document.createElement("summary"); summary.className = "collapsible-trigger"; const copy = document.createElement("span"); copy.textContent = label; const chevron = icon("chevron"); chevron.classList.add("collapsible-chevron"); summary.append(copy, chevron); return summary; }
function unavailableImage(parent) { const copy = document.createElement("span"); copy.textContent = "No image available"; parent.replaceChildren(icon("image"), copy); }
const jobState = (status) => ({ completed: "positive", healthy: "positive", available: "positive", missing: "caution", degraded: "caution", invalid: "negative", failed: "negative", cancelled: "caution", active: "running", queued: "running", preparing: "running", running: "running", saving: "running", "loading-model": "running" }[status] || "caution");
const active = (job) => job && ["queued", "preparing", "loading-model", "running", "saving", "active"].includes(job.status);
function setError(node, message = "") { node.textContent = message; node.hidden = !message; }
function say(message) { const sequence = ++toastSequence; window.clearTimeout(toastTimer); el.announce.textContent = ""; if (!message) return; queueMicrotask(() => { if (sequence !== toastSequence) return; if (window.toast?.show) { window.toast.show({ title: message, duration: 5000 }); } else { el.announce.textContent = message; toastTimer = window.setTimeout(() => { el.announce.textContent = ""; }, 5000); } }); }
function text(value, fallback = "Unavailable") { return typeof value === "string" && value ? value : fallback; }
function size(bytes) { const value = Number(bytes); if (!Number.isFinite(value) || value < 0) return "Unavailable"; const units = ["B", "KiB", "MiB", "GiB", "TiB"]; let index = 0, amount = value; while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index += 1; } return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`; }
function elapsed(startedAt) { const start = Date.parse(startedAt); if (!Number.isFinite(start)) return "Elapsed time unavailable"; return `Elapsed ${Math.max(0, Math.floor((Date.now() - start) / 1000))}s`; }
function engineName(id) { return state.engines.find((engine) => engine.id === id)?.name || id; }
const modelNames = { "vgg19-imagenet": "VGG19 ImageNet", "clip-vit-b-32": "CLIP ViT-B/32", "vqgan-decoder": "VQGAN decoder", "vqgan-codebook": "VQGAN codebook", "biggan-generator": "BigGAN generator" };
function modelName(model) { return model.name || modelNames[model.id] || model.id.replaceAll("-", " "); }
function jobParameters(job) { try { const value = typeof job.parameters === "string" ? JSON.parse(job.parameters) : job.parameters; return value && typeof value === "object" && !Array.isArray(value) ? value : {}; } catch { return {}; } }
function jobPrompt(job) { const parameters = jobParameters(job); return typeof parameters.prompt === "string" && parameters.prompt.trim() ? parameters.prompt : ""; }
const relativeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
function absoluteTime(value) { const date = new Date(value); return Number.isNaN(date.getTime()) ? "Time unavailable" : date.toLocaleString(); }
function relativeTime(value) { const time = new Date(value).getTime(); if (!Number.isFinite(time)) return "Time unavailable"; const seconds = Math.round((time - Date.now()) / 1000); const units = [[31536000, "year"], [2592000, "month"], [604800, "week"], [86400, "day"], [3600, "hour"], [60, "minute"]]; for (const [sizeInSeconds, unit] of units) if (Math.abs(seconds) >= sizeInSeconds) return relativeFormatter.format(Math.round(seconds / sizeInSeconds), unit); return relativeFormatter.format(seconds, "second"); }
function historyMatches(job) { if (state.historyFilter === "all") return true; if (state.historyFilter === "active") return active(job); return String(job.status || "").toLowerCase() === state.historyFilter; }
function defaultImageEngine() { const candidates = state.engines.filter((engine) => engine.type === "image-to-image" && engine.enabled !== false && engine.capabilities?.source_image && ready(engine)); return candidates[0]?.id || "deep-image-prior"; }
function installerActive(operation) { return operation?.status === "running"; }
function artifact(job, path) { return `/artifacts/${encodeURIComponent(job.id)}/${String(path).split("/").map(encodeURIComponent).join("/")}`; }
// An explicit no-content success is a real result: job deletion answers 204 with
// an empty body. Every other successful response must decode, so a truncated or
// malformed 200 cannot reach a caller as an empty object that erases known job
// state. A cancellation the application itself caused is not a server fault and
// must stay an AbortError so the callers keep suppressing it. An unsuccessful
// non-JSON response keeps the generic status message.
function api(path, options = {}, key = path) { const method = String(options.method || "GET").toUpperCase(); const mutation = !["GET", "HEAD"].includes(method); if (mutation && state.mutations[key]) return state.mutations[key]; if (!mutation && !state.mutations[key]) state.controllers[key]?.abort(); const controller = new AbortController(); state.controllers[key] = controller; const version = (state.request[key] || 0) + 1; state.request[key] = version; const request = fetch(path, { ...options, signal: controller.signal }).then(async (r) => { let data = {}; try { if (r.status !== 204 && method !== "HEAD") data = await r.json(); } catch (cause) { if (cause?.name === "AbortError" || controller.signal.aborted) throw cause; if (r.ok) throw new Error(`Unreadable server response (${r.status})`); } if (!r.ok) { const error = new Error(data.error?.message || `Request failed (${r.status})`); error.code = data.error?.code; throw error; } if (!mutation && state.request[key] !== version) throw new DOMException("Stale response", "AbortError"); return data; }).finally(() => { if (state.controllers[key] === controller) delete state.controllers[key]; if (state.mutations[key] === request) delete state.mutations[key]; }); if (mutation) state.mutations[key] = request; return request; }
async function busy(control, task) { if (control?.disabled) return; if (control) { control.disabled = true; control.setAttribute("aria-busy", "true"); } try { return await task(); } finally { if (control?.isConnected) { control.removeAttribute("aria-busy"); if (control === el.cancel) control.disabled = !active(state.currentJob); else if (control === el.rerun) control.disabled = !state.currentJob || active(state.currentJob); else if (control === el.useFrame) control.disabled = !state.currentJob || !state.selectedPreview; else control.disabled = false; } } }
function empty(parent, message) { parent.replaceChildren(); const node = document.createElement("p"); node.className = "empty-note"; node.textContent = message; parent.append(node); }
function retryState(parent, message, callback) { const wrap = document.createElement("div"); wrap.className = "error-state"; const textNode = document.createElement("p"); textNode.textContent = message; wrap.append(textNode, button("Retry", "secondary", callback)); parent.replaceChildren(wrap); }
function option(value, label) { const node = document.createElement("option"); node.value = value; node.textContent = label; return node; }
function requiredModels(engine) { return engine.models || []; }
function ready(engine) { return engine && engine.enabled !== false && requiredModels(engine).every((id) => state.modelStatuses[id] === "available"); }
function unavailableModels(engine) { return requiredModels(engine).filter((id) => state.modelStatuses[id] !== "available"); }
function setModels(data) { const models = Array.isArray(data) ? data : data.items || []; state.modelStatuses = Object.fromEntries(models.map((model) => [model.id, model.status])); if (state.engines.length) renderEngineFields(); return models; }
function noReadyEngines() { const message = document.createElement("p"); message.className = "empty-note"; message.append("No ready ", state.mode.replaceAll("-", " "), " engines. Check "); const link = document.createElement("a"); link.href = "#models"; link.textContent = "Models"; message.append(link, " to install or verify required models."); el.fields.replaceChildren(message); }
function showInitError(message) { rememberEngineDraft(); renderedEngine = ""; el.submit.disabled = true; empty(el.fields, message); el.fields.append(button("Retry", "secondary", () => init())); }
function labelField(name, control, title = name, description = "") { const wrap = document.createElement("div"); wrap.className = "text-field"; if (control.tagName === "TEXTAREA") wrap.classList.add("field-wide"); control.id = `field-${control.name || name}`; const label = document.createElement("label"); label.className = "text-field-label"; label.htmlFor = control.id; label.textContent = title; wrap.append(label, control); if (description) { const text = document.createElement("p"); text.id = `${control.id}-description`; text.className = "text-field-description"; text.textContent = description; const describedBy = control.getAttribute("aria-describedby"); control.setAttribute("aria-describedby", describedBy ? `${describedBy} ${text.id}` : text.id); wrap.append(text); } return wrap; }
function schemaControl(name, schema) { const type = schema.type || "string"; let control; if (schema.enum || schema.options) { control = document.createElement("select"); control.className = "select"; for (const value of schema.enum || schema.options) control.append(option(value, value)); } else if (type === "boolean") { control = document.createElement("input"); control.type = "checkbox"; control.className = "checkbox"; control.checked = Boolean(schema.default); } else if (type === "string" && (schema.format === "textarea" || name === "prompt")) { control = document.createElement("textarea"); control.className = "textarea"; control.rows = 3; } else { control = document.createElement("input"); control.className = "text-field-input"; control.type = ["integer", "number"].includes(type) ? "number" : "text"; if (["integer", "number"].includes(type)) control.step = type === "integer" ? "1" : "any"; }
  control.name = name; if (schema.default !== undefined && control.type !== "checkbox") control.value = typeof schema.default === "number" ? String(Number(schema.default.toFixed(6))) : schema.default; if (schema.min !== undefined) control.min = schema.min; if (schema.max !== undefined) control.max = schema.max; if (control.type === "checkbox") { control.id = `field-${name}`; const wrap = document.createElement("div"); wrap.className = "checkbox-item-block"; const label = document.createElement("label"); label.htmlFor = control.id; label.textContent = schema.title || name.replaceAll("_", " "); wrap.append(control, label); if (schema.description) { const text = document.createElement("p"); text.id = `${control.id}-description`; text.className = "checkbox-description"; text.textContent = schema.description; control.setAttribute("aria-describedby", text.id); wrap.append(text); } return wrap; } return labelField(name, control, schema.title || name.replaceAll("_", " "), schema.description || "");
}
// Input intent revisions are never reset: accepted requests may still finish.
function nextInputRevision(kind) { const revision = String(Number(state.inputRevisions[kind] || 0) + 1); state.inputRevisions[kind] = revision; return revision; }
async function upload(file, kind, preview) { const body = new FormData(); body.append("image", file); const localURL = URL.createObjectURL(file); const revision = nextInputRevision(kind); preview.dataset.uploadRevision = revision; preview.src = localURL; preview.hidden = false; preview.dataset.token = ""; state.pendingInputs[kind] = ""; const input = uploadInput(kind); if (input) syncUploadConstraint(input); state.uploads += 1; el.submit.disabled = true; try { const result = await api("/api/uploads", { method: "POST", body }, `upload-${kind}-${revision}`); if (state.inputRevisions[kind] !== revision) return; renderInputToken(kind, result.token || result.url || ""); } catch (error) { if (state.inputRevisions[kind] !== revision) return; renderInputToken(kind, ""); throw error; } finally { URL.revokeObjectURL(localURL); state.uploads -= 1; el.submit.disabled = state.uploads > 0; } }
function uploadInput(kind) { return el.form.querySelector(`[name="${kind}"]`); }
function inputPreview(kind) { return uploadInput(kind)?.parentElement.querySelector("img"); }
function syncUploadConstraint(input, token = "") { const required = input.dataset.required === "true"; const nativeRequired = required && !token; input.required = nativeRequired; input.setAttribute("aria-required", String(nativeRequired)); }
function renderInputToken(kind, token) { const resolvedToken = typeof token === "string" ? token : ""; state.pendingInputs[kind] = resolvedToken; const input = uploadInput(kind); if (input) syncUploadConstraint(input, resolvedToken); const preview = inputPreview(kind); if (!preview) return; preview.dataset.token = resolvedToken; const url = tokenPreviewURL(resolvedToken); if (url) { preview.src = url; preview.hidden = false; } else { preview.removeAttribute("src"); preview.hidden = true; } }
function tokenPreviewURL(token) { const input = /^inputs\/([a-f0-9]{32})\.png$/.exec(token); if (input) return `/api/uploads/${encodeURIComponent(input[1])}`; const parts = token.split("/"); if (parts[0] === "workspace" && parts[1] === "jobs" && parts.length >= 4) return `/artifacts/${parts.slice(2).map(encodeURIComponent).join("/")}`; return ""; }
function uploadField(kind, title, required = false) { const input = document.createElement("input"); input.className = "file-input"; input.type = "file"; input.name = kind; input.accept = "image/png,image/jpeg"; input.setAttribute("aria-required", String(required)); const preview = document.createElement("img"); preview.className = "upload-preview"; preview.alt = `${title} preview`; preview.hidden = true; const pending = state.pendingInputs[kind]; if (pending) { preview.dataset.token = pending; preview.src = tokenPreviewURL(pending); preview.hidden = !preview.src; } const wrap = labelField(kind, input, title, "PNG or JPEG. Choose a file, drop it here, or paste an image while this area is focused."); wrap.classList.add("upload-box", "upload-dropzone"); wrap.tabIndex = 0; wrap.setAttribute("role", "group"); wrap.setAttribute("aria-label", `${title} upload area`); const status = document.createElement("p"); status.className = "upload-status meta-text"; status.setAttribute("role", "status"); status.setAttribute("aria-live", "polite"); wrap.append(preview, status); const begin = (file) => { if (input.disabled) return; if (!(file instanceof File) || !["image/png", "image/jpeg"].includes(file.type)) { status.textContent = "Choose a PNG or JPEG image."; return; } input.disabled = true; status.textContent = `Uploading ${file.name || "image"}…`; upload(file, kind, preview).then(() => { status.textContent = "Image uploaded."; }).catch((error) => { if (error.name !== "AbortError") { status.textContent = error.message; setError(el.error, error.message); } }).finally(() => { if (input.isConnected) input.disabled = false; }); }; input.addEventListener("change", () => { if (input.files[0]) begin(input.files[0]); }); ["dragenter", "dragover"].forEach((eventName) => wrap.addEventListener(eventName, (event) => { event.preventDefault(); wrap.dataset.dragOver = "true"; })); ["dragleave", "drop"].forEach((eventName) => wrap.addEventListener(eventName, (event) => { event.preventDefault(); if (eventName === "drop") begin(event.dataTransfer?.files?.[0]); wrap.removeAttribute("data-drag-over"); })); wrap.addEventListener("paste", (event) => { const file = [...(event.clipboardData?.items || [])].map((item) => item.getAsFile?.()).find(Boolean); if (file) { event.preventDefault(); begin(file); } }); wrap.addEventListener("keydown", (event) => { if (event.target !== wrap || !["Enter", " "].includes(event.key)) return; event.preventDefault(); input.click(); }); return wrap; }
function uploadRequired(engine, name) { return Array.isArray(engine.required_inputs) && engine.required_inputs.includes(name); }
function requiredUploadField(kind, title, required) { const field = uploadField(kind, title, required); const input = field.querySelector('input[type="file"]'); if (input) { input.dataset.required = String(required); syncUploadConstraint(input, state.pendingInputs[kind] || ""); } return field; }
function renderEngineForm() {
  el.fields.replaceChildren();
  const engines = state.engines.filter((engine) => engine.type === state.mode && engine.enabled !== false);
  if (!engines.length) return empty(el.fields, `No ${state.mode.replaceAll("-", " ")} engines are available.`);
  const select = document.createElement("select");
  select.className = "select";
  select.name = "engine";
  engines.forEach((engine) => select.append(option(engine.id, engine.name || engine.id)));
  if (engines.some((engine) => engine.id === state.selectedEngine[state.mode])) select.value = state.selectedEngine[state.mode];
  state.selectedEngine[state.mode] = select.value;
  select.addEventListener("change", () => { state.selectedEngine[state.mode] = select.value; renderEngineFields(); el.fields.querySelector('select[name="engine"]')?.focus({ preventScroll: true }); });
  const engine = engines.find((item) => item.id === select.value);
  el.fields.append(labelField("engine", select, "Engine"));
  if (engine.description) { const description = document.createElement("p"); description.id = "engine-description"; description.className = "engine-description"; description.textContent = engine.description; select.setAttribute("aria-describedby", description.id); el.fields.append(description); }
  const groups = { Basic: [], Advanced: [], Expert: [] };
  const order = ["prompt", "iterations", "width", "height"];
  Object.entries(engine.parameters || {}).sort(([a], [b]) => (order.includes(a) ? order.indexOf(a) : order.length) - (order.includes(b) ? order.indexOf(b) : order.length)).forEach(([name, schema]) => {
    const group = String(schema.group || schema.level || schema.ui_group || "Basic").toLowerCase();
    const level = /(_path|checkpoint)$/.test(name) || group.includes("expert") ? "Expert" : group.includes("advanced") ? "Advanced" : "Basic";
    groups[level].push(schemaControl(name, schema));
  });
  groups.Advanced.push(labelField("seed", Object.assign(document.createElement("input"), { className: "text-field-input", name: "seed", type: "number", step: "1", placeholder: "Random" }), "Seed", "Leave empty for a random seed."));
  if (engine.capabilities?.source_image) groups.Basic.push(requiredUploadField("source_image", "Source image", uploadRequired(engine, "source_image")));
  if (engine.capabilities?.style_image) groups[uploadRequired(engine, "style_image") ? "Basic" : "Advanced"].push(requiredUploadField("style_image", "Style image", uploadRequired(engine, "style_image")));
  for (const [name, fields] of Object.entries(groups)) {
    if (!fields.length) continue;
    const group = name === "Basic" ? document.createElement("fieldset") : document.createElement("details");
    group.className = `field-group field-group-${name.toLowerCase()}${name === "Basic" ? "" : " collapsible"}`;
    group.dataset.parameterGroup = name;
    if (name === "Basic") {
      const title = document.createElement("legend");
      title.className = "field-group-title";
      title.textContent = "Image settings";
      group.append(title);
    } else {
      const title = disclosureSummary(name === "Advanced" ? "Advanced settings" : "Runtime settings");
      group.open = false;
      group.append(title);
    }
    const grid = document.createElement("div");
    grid.className = "field-grid";
    if (name !== "Basic") grid.classList.add("collapsible-content");
    fields.forEach((field) => grid.append(field));
    group.append(grid);
    el.fields.append(group);
  }
  restoreEngineDraft(engine);
  renderedEngine = engine.id;
}
function renderEngineFields() { rememberEngineDraft(); const focusedName = document.activeElement && el.fields.contains(document.activeElement) ? document.activeElement.name : ""; const engines = state.engines.filter((engine) => engine.type === state.mode && engine.enabled !== false); const readyEngines = engines.filter(ready); if (!readyEngines.length) { noReadyEngines(); renderedEngine = ""; el.engineStatus.textContent = "No ready engines."; el.submit.disabled = true; return; } if (!ready(engines.find((engine) => engine.id === state.selectedEngine[state.mode]))) state.selectedEngine[state.mode] = readyEngines[0].id; renderEngineForm(); const select = el.fields.querySelector('select[name="engine"]'); [...select.options].forEach((node) => { const engine = engines.find((item) => item.id === node.value); const missing = unavailableModels(engine); if (missing.length) { node.disabled = true; node.textContent = `${engine.name || engine.id} (${missing.join(", ")} unavailable)`; } }); if (focusedName) [...el.fields.querySelectorAll("[name]")].find((node) => node.name === focusedName)?.focus({ preventScroll: true }); el.submit.disabled = state.uploads > 0; el.engineStatus.textContent = `${readyEngines.length} ${state.mode.replaceAll("-", " ")} engines ready.`; }
function renderCurrent(job) { const previous = state.currentJob; const frames = job.preview_frames || job.frames || (previous && previous.id === job.id ? previous.preview_frames || [] : []); state.currentJob = { ...job, preview_frames: frames }; if (!state.previewPinned) state.selectedPreview = job.status === "completed" ? job.final_path : job.preview_path || job.final_path; const total = Number(job.progress_total || 0); const step = Number(job.progress_step || 0); const statusText = String(job.status || "unknown").replaceAll("-", " "); const statusKey = `${job.id}:${statusText}`; if (state.jobStatusKey !== statusKey) { el.status.textContent = statusText; el.status.dataset.state = jobState(job.status); state.jobStatusKey = statusKey; } el.progress.max = Math.max(total, 1); el.progress.value = Math.min(step, Math.max(total, 1)); el.progressLabel.textContent = `${step} / ${total}`; el.progressRow.hidden = !total; const path = state.selectedPreview || job.preview_path || job.final_path; if (path) { state.selectedPreview = path; el.preview.src = `${artifact(job, path)}?step=${step}`; el.preview.hidden = false; el.empty.hidden = true; } else { el.preview.hidden = true; el.empty.hidden = false; } el.cancel.disabled = !active(job); el.rerun.disabled = active(job); el.export.disabled = !["completed", "failed", "cancelled"].includes(job.status); el.downloadFrame.disabled = !path; el.useFrame.disabled = !path; el.unpin.hidden = !state.previewPinned; setError(el.jobError, job.error_message); renderTimeline(state.currentJob); }
function renderTimeline(job) { if (state.timelineJob !== job.id) { state.timelineJob = job.id; el.timeline.replaceChildren(); } const frames = job.frames || job.preview_frames || []; const paths = [...new Set([...frames.map((x) => typeof x === "string" ? x : x.path).filter(Boolean), job.preview_path, job.final_path].filter(Boolean))]; const buttons = new Map([...el.timeline.children].map((button) => [button.dataset.path, button])); for (const [path, button] of buttons) { if (!paths.includes(path)) button.remove(); } paths.forEach((path, index) => { let button = buttons.get(path); if (!button) { button = document.createElement("button"); button.className = "timeline-item"; button.type = "button"; button.dataset.path = path; const step = Number(String(path).match(/(\d+)\.png$/)?.[1] || index + 1); button.title = `Step ${step}`; button.setAttribute("aria-label", `Step ${step}`); const image = document.createElement("img"); image.src = artifact(job, path); image.alt = ""; image.loading = "lazy"; image.decoding = "async"; button.append(image); button.addEventListener("click", () => { state.selectedPreview = path; state.previewPinned = true; renderCurrent(state.currentJob || job); }); el.timeline.append(button); } button.setAttribute("aria-pressed", String(path === state.selectedPreview)); }); }
function activeJobIDs() { return [...new Map([...state.jobs, state.currentJob].filter(active).map((job) => [job.id, job])).keys()]; }
function scheduleJobRefresh() { window.clearTimeout(state.jobPoll); state.jobPoll = null; if (document.hidden || !activeJobIDs().length) return; const delay = state.connection.stream === "ok" ? 10000 : 5000; state.jobPoll = window.setTimeout(refreshActiveJobs, delay); }
async function refreshActiveJobs() { const ids = activeJobIDs().slice(0, 4); if (!ids.length) return; await Promise.all(ids.map(refreshJob)); await loadJobs(); scheduleJobRefresh(); }
function reconcileJobs(jobs, selectFirst = false) { const current = state.currentJob && jobs.find((job) => job.id === state.currentJob.id); if (current) renderCurrent(current); else if (selectFirst && !state.currentJob && jobs[0]) { renderCurrent(jobs[0]); refreshJob(jobs[0].id); } scheduleJobRefresh(); }
async function loadJobs(selectFirst = false, more = false) { const offset = more ? state.jobs.length : 0; const key = more ? `jobs-${offset}` : "jobs"; const loading = !state.jobs.length; if (loading) { el.history.setAttribute("aria-busy", "true"); empty(el.history, "Loading history…"); } try { const data = await api(`/api/jobs?limit=100&offset=${offset}`, {}, key); const jobs = Array.isArray(data) ? data : data?.items || []; if (more) { const existing = new Map(state.jobs.map((job) => [job.id, job])); jobs.forEach((job) => existing.set(job.id, job)); state.jobs = [...existing.values()]; } else { state.jobs = jobs; } state.jobHasMore = jobs.length === 100; renderHistory(); optimizeHistoryImages(); reconcileJobs(state.jobs, selectFirst); return jobs; } catch (error) { if (error.name !== "AbortError") retryState(el.history, error.message, () => loadJobs(true)); return []; } finally { el.history.removeAttribute("aria-busy"); } }
function scheduleJobListRefresh() { window.clearTimeout(state.jobListDebounce); state.jobListDebounce = window.setTimeout(() => { state.jobListDebounce = null; loadJobs(); }, 2000); }
async function refreshJob(id) { try { const job = await api(`/api/jobs/${encodeURIComponent(id)}`, {}, `job-${id}`); if (state.currentJob?.id === id) renderCurrent(job); return job; } catch (error) { if (error.name !== "AbortError" && state.currentJob?.id === id) setError(el.jobError, error.message); return null; } }
function button(text, variant, callback, disabled = false) { const node = document.createElement("button"); node.type = "button"; node.className = "btn"; node.dataset.variant = variant; node.textContent = text; node.disabled = disabled; node.addEventListener("click", () => callback(node)); return node; }
function renderHistory() {
  // Keep dialog triggers connected while a modal owns focus.
  if (el.detail.open || el.confirm.open) return;
  const openActions = new Set([...el.history.querySelectorAll('.job-card details[open]')].map(details => details.closest('.job-card').dataset.jobId));
  const focused = document.activeElement;
  const focusedCard = el.history.contains(focused) ? focused.closest('.job-card') : null;
  const focusKey = focusedCard ? { job: focusedCard.dataset.jobId, label: focused.textContent } : null;
  el.history.replaceChildren();
  if (!state.jobs.length) return empty(el.history, "No generations yet.");
  state.jobs.forEach((job) => {
    const card = document.createElement("article");
    card.className = "card job-card";
    card.dataset.jobId = job.id;
    const thumb = document.createElement("div");
    thumb.className = "job-thumb";
    const path = job.final_path || job.preview_path;
    if (path) {
      const image = document.createElement("img");
      image.alt = "Generated artifact";
      image.loading = "lazy";
      image.decoding = "async";
      image.addEventListener("error", () => { if (image.isConnected) unavailableImage(thumb); }, { once: true });
      image.src = artifact(job, path);
      thumb.append(image);
    } else unavailableImage(thumb);
    const body = document.createElement("div");
    body.className = "card-content job-card-body";
    const top = document.createElement("div");
    top.className = "card-top";
    const title = document.createElement("strong");
    title.textContent = engineName(job.engine);
    const status = document.createElement("span");
    status.className = "badge";
    status.dataset.state = jobState(job.status);
    status.textContent = job.status;
    top.append(title, status);
    const meta = document.createElement("p");
    meta.className = "meta-text";
    meta.textContent = `${relativeTime(job.created_at)} · seed ${job.seed}`;
    meta.title = absoluteTime(job.created_at);
    const actions = document.createElement("div");
    actions.className = "card-actions";
    const isActive = active(job);
    const open = button("Open", "secondary", () => openDetail(job));
    open.dataset.dialogTrigger = "detail-dialog";
    const remove = button("Delete", "ghost", () => askDelete(job.id), isActive);
    remove.dataset.alertDialogTrigger = "confirm-dialog";
    actions.append(open, historyActionButton("duplicate", "Run again", (control) => duplicate(job, control), isActive));
    const more = document.createElement("details");
    more.className = "collapsible";
    more.open = openActions.has(job.id);
    more.append(disclosureSummary("More actions"));
    const extra = document.createElement("div");
    extra.className = "collapsible-content card-actions";
    extra.append(historyActionButton("input", "Use as input", (control) => useAsInput(job, "", control), job.status !== "completed"), button("Export", "ghost", () => { location.href = `/api/jobs/${encodeURIComponent(job.id)}/export`; }, isActive), remove);
    more.append(extra);
    body.append(top, meta);
    const prompt = jobPrompt(job);
    if (prompt) { const line = document.createElement("p"); line.className = "card-prompt"; line.textContent = prompt; body.append(line); }
    body.append(actions, more);
    card.append(thumb, body);
    el.history.append(card);
    applyHistoryActionState(card);
  });
  if (state.jobHasMore) { const more = document.createElement("div"); more.className = "history-more"; more.append(button("Load more", "secondary", (control) => busy(control, () => loadJobs(false, true)))); el.history.append(more); }
  if (focusKey) queueMicrotask(() => [...el.history.querySelectorAll('.job-card')].find(card => card.dataset.jobId === focusKey.job)?.querySelectorAll('button, summary').forEach(control => { if (control.textContent === focusKey.label) control.focus({ preventScroll: true }); }));
}
const renderHistoryCards = renderHistory;
renderHistory = function renderFilteredHistory() { if (el.detail.open || el.confirm.open) return; renderHistoryCards(); const cards = [...el.history.querySelectorAll(".job-card")]; let visible = 0; cards.forEach((card, index) => { const job = state.jobs[index]; const matches = Boolean(job && historyMatches(job)); card.hidden = !matches; if (matches) visible += 1; const meta = card.querySelector(".meta-text"); if (meta && job) { meta.textContent = `${relativeTime(job.created_at)} · seed ${job.seed}`; meta.title = absoluteTime(job.created_at); } }); el.history.querySelector(".history-filter-empty")?.remove(); if (state.jobs.length && !visible) { const emptyState = document.createElement("p"); emptyState.className = "empty-note history-filter-empty"; emptyState.textContent = "No jobs match this status filter."; el.history.append(emptyState); } if (el.historySummary) el.historySummary.textContent = state.jobs.length ? `${visible} of ${state.jobs.length} jobs` : ""; };
function optimizeHistoryImages() { el.history.querySelectorAll("img").forEach((image) => { image.loading = "lazy"; image.decoding = "async"; }); }
function historyActionButton(action, label, callback, unavailable) { const control = button(label, "ghost", callback, unavailable); control.dataset.jobAction = action; control.dataset.unavailable = String(unavailable); return control; }
function applyHistoryActionState(card) {
  card.querySelectorAll("[data-job-action]").forEach((control) => {
    const action = control.dataset.jobAction, status = state.historyActions.get(`${card.dataset.jobId}:${action}`);
    control.disabled = control.dataset.unavailable === "true" || status?.pending === true;
    if (status?.pending) control.setAttribute("aria-busy", "true"); else control.removeAttribute("aria-busy");
    let error = [...card.querySelectorAll(".history-action-error")].find(node => node.dataset.jobActionError === action);
    if (!error && status?.error) { error = document.createElement("p"); error.className = "field-error history-action-error"; error.dataset.jobActionError = action; error.setAttribute("role", "alert"); card.querySelector(".job-card-body").append(error); }
    if (error) setError(error, status?.error || "");
  });
}
function syncHistoryActionState(id) { el.history.querySelectorAll(".job-card").forEach(card => { if (card.dataset.jobId === id) applyHistoryActionState(card); }); }
async function jobAction(job, action, control, task) {
  const key = `${job.id}:${action}`;
  if (control?.disabled || state.historyActions.get(key)?.pending) return;
  const status = { pending: true, error: "" }; state.historyActions.set(key, status);
  const inHistory = Boolean(control?.closest(".job-card"));
  if (!inHistory) setError(el.jobError);
  // Busy owns the initiating control; the job/action record owns its successors.
  try { return await busy(control, async () => { syncHistoryActionState(job.id); return await task(); }); }
  catch (error) { status.error = error.message; if (!inHistory) setError(el.jobError, error.message); }
  finally { status.pending = false; syncHistoryActionState(job.id); }
}
async function duplicate(job, control) { return jobAction(job, "duplicate", control, async () => { const next = await api(`/api/jobs/${encodeURIComponent(job.id)}/duplicate`, { method: "POST" }, `duplicate-${job.id}`); state.selectedPreview = null; state.previewPinned = false; renderCurrent(next); scheduleJobRefresh(); location.hash = "generate"; say("New job queued."); }); }
async function useAsInput(job, path = "", control) { return jobAction(job, "input", control, async () => { const revision = nextInputRevision("source_image"); try { const query = path ? `?path=${encodeURIComponent(path)}` : ""; const result = await api(`/api/jobs/${encodeURIComponent(job.id)}/use-as-input${query}`, { method: "POST" }, `use-input-${job.id}-${path}`); if (state.inputRevisions.source_image !== revision) return; renderInputToken("source_image", result.token); state.selectedEngine["image-to-image"] = defaultImageEngine(); selectMode("image-to-image"); location.hash = "generate"; say("Job image is ready as the source input."); } catch (error) { if (state.inputRevisions.source_image === revision) throw error; } }); }
function openDetail(job) {
  $("#detail-title").textContent = `${engineName(job.engine)} job`;
  const image = $("#detail-image");
  const path = job.final_path || job.preview_path;
  image.hidden = !path;
  el.detailViewport.tabIndex = image.hidden ? -1 : 0;
  el.detailEmpty.hidden = Boolean(path);
  el.detailEmpty.textContent = "No image is available for this job.";
  if (path) image.src = artifact(job, path);
  else image.removeAttribute("src");
  const meta = $("#detail-meta");
  meta.replaceChildren();
  const rows = [["Status", job.status], ["Seed", job.seed], ["Created", absoluteTime(job.created_at)], ["Device", job.runtime_device || "—"], ["Precision", job.runtime_precision || "—"]];
  const prompt = jobPrompt(job);
  if (prompt) rows.push(["Prompt", prompt]);
  rows.forEach(([key, value]) => { const dt = document.createElement("dt"); dt.textContent = key; const dd = document.createElement("dd"); dd.textContent = value; meta.append(dt, dd); });
  const parameters = jobParameters(job);
  const entries = Object.entries(parameters).filter(([key, value]) => key !== "prompt" && !(typeof value === "string" && /^(inputs\/|workspace\/)/.test(value)));
  if (entries.length) {
    const dt = document.createElement("dt"); dt.textContent = "Parameters";
    const dd = document.createElement("dd");
    const details = document.createElement("details");
    details.className = "collapsible detail-parameters";
    details.append(disclosureSummary("View parameters"));
    const list = document.createElement("dl");
    list.className = "collapsible-content detail-parameter-list";
    entries.forEach(([key, value]) => { const dt = document.createElement("dt"); dt.textContent = key; const dd = document.createElement("dd"); dd.textContent = typeof value === "object" ? JSON.stringify(value) : String(value); list.append(dt, dd); });
    details.append(list); dd.append(details); meta.append(dt, dd);
  }
}
function setDetailZoom(value) { state.detailZoom = Math.min(3, Math.max(1, Math.round(value * 10) / 10)); el.detailImage.style.transform = `scale(${state.detailZoom})`; el.zoomLevel.textContent = `${Math.round(state.detailZoom * 100)}%`; const hasImage = !el.detailImage.hidden && Boolean(el.detailImage.getAttribute("src")); el.zoomOut.disabled = !hasImage || state.detailZoom <= 1; el.zoomIn.disabled = !hasImage || state.detailZoom >= 3; el.zoomReset.disabled = !hasImage || state.detailZoom === 1; }
const openDetailBase = openDetail;
openDetail = function openDetailWithZoom(job) { openDetailBase(job); el.detailViewport.scrollTo(0, 0); setDetailZoom(1); };
el.detailImage.addEventListener("error", () => { el.detailImage.hidden = true; el.detailImage.removeAttribute("src"); el.detailViewport.tabIndex = -1; el.detailEmpty.hidden = false; el.detailEmpty.textContent = "This job’s image is unavailable."; setDetailZoom(1); });
el.preview.addEventListener("error", () => { el.preview.hidden = true; el.preview.removeAttribute("src"); el.empty.hidden = false; el.empty.textContent = "This preview image is unavailable."; });
function setDeletePending(pending) { state.deletePending = pending; el.confirm.setAttribute("aria-busy", String(pending)); el.confirm.querySelectorAll("[data-alert-dialog-close]").forEach((node) => { node.disabled = pending; }); el.confirmProgress.hidden = !pending; el.confirmProgress.tabIndex = pending ? 0 : -1; if (pending) el.confirmProgress.focus({ preventScroll: true }); }
function askDelete(id) { state.deleteId = id; setError(el.confirmError); setDeletePending(false); }
async function deleteJob(control) { const id = state.deleteId; if (!id || state.deletePending) return; return busy(control, async () => { setDeletePending(true); try { await api(`/api/jobs/${encodeURIComponent(id)}`, { method: "DELETE" }, `delete-${id}`); if (state.currentJob?.id === id) state.currentJob = null; el.confirm.close(); await loadJobs(true); say("Job deleted."); } catch (error) { setError(el.confirmError, error.message); el.confirmError.focus({ preventScroll: true }); } finally { if (el.confirm.open) setDeletePending(false); } }); }
async function loadModels(initialData = null) {
  setError(el.modelsError);
  if (!el.models.children.length) empty(el.models, "Loading models…");
  try {
    const data = initialData || await api("/api/models", {}, "models");
    const models = setModels(data);
    el.models.replaceChildren();
    if (!models.length) return empty(el.models, "No models are registered.");
    const families = new Map();
    models.forEach((model) => { const family = model.family || "Other"; families.set(family, [...(families.get(family) || []), model]); });
    families.forEach((models, family) => {
      const section = document.createElement("section"); section.className = "app-section model-family";
      const header = document.createElement("div"); header.className = "app-section-header";
      const title = document.createElement("h2"); title.className = "app-section-title"; title.textContent = family;
      header.append(title);
      const list = document.createElement("ul"); list.className = "app-dense-list";
      models.forEach((model) => {
        const row = document.createElement("li"); row.className = "app-dense-row app-dense-row--plain";
        const copy = document.createElement("div"); copy.className = "app-dense-copy";
        const heading = document.createElement("div"); heading.className = "app-dense-heading";
        const name = document.createElement("strong"); name.textContent = modelName(model);
        const status = document.createElement("span"); status.className = "badge"; status.dataset.state = jobState(model.status); status.textContent = model.status || "missing";
        heading.append(name, status); copy.append(heading);
        const actions = document.createElement("div"); actions.className = "app-dense-actions";
        actions.append(button("Verify", "secondary", (control) => busy(control, async () => {
          setError(el.modelsError);
          try { await api(`/api/models/${encodeURIComponent(model.id)}/verify`, { method: "POST" }, `verify-${model.id}`); await loadModels(); }
          catch (error) { setError(el.modelsError, error.message); say(error.message); }
        })));
        row.append(copy, actions); list.append(row);
      });
      section.append(header, list); el.models.append(section);
    });
  } catch (error) { if (error.name !== "AbortError") { setError(el.modelsError, error.message); retryState(el.models, error.message, () => loadModels()); } }
}
el.installer.setAttribute("aria-label", "Install Bundle B");
el.installer.removeAttribute("aria-labelledby");
function appendInstallerLink(parent, label, rawURL) { try { const url = new URL(rawURL); if (!/^https?:$/.test(url.protocol)) throw new Error("unsupported protocol"); const link = document.createElement("a"); link.className = "bundle-link"; link.href = url.href; link.target = "_blank"; link.rel = "noopener noreferrer"; link.append(document.createTextNode(label || url.hostname), icon("external-link")); parent.append(link); } catch { parent.append(document.createTextNode(label || "Link unavailable")); } }
function installerRow(label, value) { const row = document.createElement("div"); row.className = "bundle-detail-row"; const key = document.createElement("span"); key.textContent = label; const content = document.createElement("span"); content.textContent = value; row.append(key, content); return row; }
function renderInstaller(data) {
  state.installer.data = data && typeof data === "object" ? data : {};
  const installer = state.installer.data, policy = installer.policy && typeof installer.policy === "object" ? installer.policy : {};
  state.installer.policyStale = false;
  syncInstallerDialogPolicy(policy);
  const catalog = installer.catalog && typeof installer.catalog === "object" ? installer.catalog : {};
  const operation = installer.operation && typeof installer.operation === "object" ? installer.operation : null;
  const capability = installer.capability && typeof installer.capability === "object" ? installer.capability : {};
  const available = installer.enabled === true && capability.available !== false;
  const running = installerActive(operation);
  const phase = running ? String(operation.phase || "") : null;
  if (phase && phase !== state.installer.lastPhase) say(`Bundle B ${phase}`);
  state.installer.lastPhase = phase;
  const focusedID = el.installer.contains(document.activeElement) ? document.activeElement.id : "";
  const disclosureOpen = el.installer.querySelector(".bundle-disclosure")?.open || false;
  const content = document.createElement("div"); content.className = "app-section-content";
  el.installer.replaceChildren(content);
  const title = document.createElement("h2"); title.className = "panel-title"; title.textContent = "Bundle B";
  const summary = document.createElement("p"); summary.className = "bundle-summary"; summary.textContent = "Six checkpoint/config files are downloaded. Two pinned conversion source trees are bundled in the image.";
  const policyNode = document.createElement("div"); policyNode.className = "bundle-policy";
  const policyTitle = document.createElement("strong"); policyTitle.textContent = "Installation policy";
  const policyText = document.createElement("p"); policyText.textContent = text(policy.text, "Policy details are unavailable."); policyNode.append(policyTitle, policyText);
  const status = document.createElement("p"); status.className = "bundle-status";
  if (running) { status.dataset.state = "running"; status.textContent = "Bundle B installation is in progress."; }
  else if (installer.installed === true || operation?.status === "succeeded") { status.dataset.state = "positive"; status.textContent = "Bundle B is installed. Replacement requires operator action or a new catalog."; }
  else if (!available) { status.dataset.state = "caution"; status.textContent = `${text(capability.message, "Checkpoint downloads are unavailable.")} An administrator can enable them with UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS=true.`; }
  else if (operation?.status) { status.dataset.state = operation.status === "failed" ? "negative" : "caution"; status.textContent = `Previous installation ${operation.status}.`; }
  else status.textContent = "Installation is optional.";
  const details = document.createElement("div"); details.className = "bundle-details"; const sources = Array.isArray(catalog.files) ? catalog.files : []; const repos = Array.isArray(catalog.repositories) ? catalog.repositories : []; const outputs = Array.isArray(catalog.outputs) ? catalog.outputs : []; [["Source files", sources, (item) => `${text(item.label, item.id || "Source")} · ${size(item.bytes)} · ${text(item.sha256, "Hash unavailable").slice(0, 12)}`], ["Pinned repositories", repos, (item) => `${text(item.label, item.id || "Repository")} · ${text(item.commit, "Revision unavailable")}`], ["Expected outputs", outputs, (item) => `${text(item.label, item.id || "Artifact")} · ${text(item.path, "Path unavailable")}`]].forEach(([heading, items, format]) => { const section = document.createElement("section"); const headingNode = document.createElement("h3"); headingNode.textContent = heading; section.append(headingNode); if (!items.length) { const missing = document.createElement("p"); missing.textContent = "Not provided by the server."; section.append(missing); } items.forEach((item) => { if (!item || typeof item !== "object") return; const row = document.createElement("div"); row.className = "bundle-source"; const main = document.createElement("span"); main.textContent = format(item); row.append(main); if (item.url) { let host = "Upstream"; try { host = new URL(item.url).hostname || host; } catch { /* The link helper displays an unavailable label. */ } appendInstallerLink(row, host, item.url); } if (heading === "Source files" && item.license_note) { const note = document.createElement("small"); note.textContent = item.license_note; row.append(note); } section.append(row); }); details.append(section); }); details.append(installerRow("Estimated disk requirement", size(catalog.estimated_disk_bytes)));
  const actions = document.createElement("div"); actions.className = "bundle-actions"; if (running) { const progress = document.createElement("progress"); progress.className = "progress"; progress.setAttribute("aria-label", "Bundle B installation progress"); const complete = Number(operation.completed_bytes), total = Number(operation.total_bytes); if (/download/i.test(String(operation.phase || "")) && Number.isFinite(complete) && Number.isFinite(total) && total > 0) { progress.max = total; progress.value = Math.max(0, Math.min(complete, total)); progress.setAttribute("aria-valuetext", `${size(complete)} of ${size(total)} downloaded`); } else progress.removeAttribute("value"); const progressText = document.createElement("p"); progressText.className = "meta-text"; progressText.setAttribute("role", "status"); progressText.textContent = `${text(operation.phase, "Working")} · ${text(operation.current_source, "Preparing source")} · ${Number.isFinite(Number(operation.completed_sources)) ? operation.completed_sources : "?"} / ${Number.isFinite(Number(operation.total_sources)) ? operation.total_sources : "?"} sources · ${size(complete)} / ${size(total)} · ${elapsed(operation.started_at)}`; const cancel = button("Cancel installation", "ghost", (control) => cancelInstaller(operation.id, control), !operation.id); cancel.id = "installer-cancel"; actions.append(progress, progressText, cancel); } else if (installer.installed !== true && operation?.status !== "succeeded") { const install = button("Install Bundle B", "primary", openInstallerDialog, !available || !policy.version); install.id = "installer-open"; install.dataset.dialogTrigger = "installer-dialog"; actions.append(install); }
  if (operation?.error_message) { const error = document.createElement("p"); error.className = "field-error"; error.setAttribute("role", "alert"); error.textContent = operation.error_message; actions.append(error); }
  if (operation?.status === "succeeded" && operation.provenance_path) details.append(installerRow("Provenance", operation.provenance_path));
  const disclosure = document.createElement("details"); disclosure.className = "collapsible bundle-disclosure"; disclosure.open = disclosureOpen;
  const trigger = disclosureSummary("Policy and source files"); trigger.id = "installer-details";
  const supporting = document.createElement("div"); supporting.className = "collapsible-content bundle-supporting"; supporting.append(summary, policyNode, details);
  disclosure.append(trigger, supporting); content.append(title, status, actions, disclosure);
  if (focusedID) queueMicrotask(() => document.getElementById(focusedID)?.focus({ preventScroll: true }));
  if (running) scheduleInstallerPoll();
}
function stopInstallerPolling() { window.clearTimeout(state.installer.poll); state.installer.poll = null; state.controllers.installer?.abort(); }
function scheduleInstallerPoll() { window.clearTimeout(state.installer.poll); if (location.hash.slice(1) !== "models" || document.hidden || !installerActive(state.installer.data?.operation)) return; state.installer.poll = window.setTimeout(() => loadInstaller(true), 1500); }
async function loadInstaller(polling = false) { if (location.hash.slice(1) !== "models" || document.hidden) return; try { const data = await api("/api/model-installer", { cache: "no-store" }, "installer"); renderInstaller(data); if (data?.operation?.status === "succeeded") loadModels(); } catch (error) { if (error.name !== "AbortError") { window.clearTimeout(state.installer.poll); retryState(el.installer, `Bundle B installer unavailable: ${error.message}`, () => loadInstaller()); } } }
function sameInstallerPolicy(a, b) { return Boolean(a && b && a.version === b.version && a.text === b.text); }
function syncInstallerConsent() { const installer = state.installer; el.installerConfirm.disabled = installer.starting || installer.policyStale || !el.installerAck.checked || !installer.displayedPolicy?.version || installer.acceptedPolicy !== installer.displayedPolicy || !sameInstallerPolicy(installer.displayedPolicy, installer.data?.policy); }
function presentInstallerPolicy(policy) { state.installer.displayedPolicy = { version: policy.version, text: policy.text }; state.installer.acceptedPolicy = null; el.installerAck.checked = false; el.installerPolicy.textContent = text(policy.text, "Policy details are unavailable."); syncInstallerConsent(); if (el.installerDialog.open) el.installerDialog.querySelector("[data-dialog-close]")?.focus({ preventScroll: true }); el.installerDialog.scrollTop = 0; }
function syncInstallerDialogPolicy(policy) { if (el.installerDialog.open && !sameInstallerPolicy(state.installer.displayedPolicy, policy)) { presentInstallerPolicy(policy); setError(el.installerError, "Installation policy changed. Review it and accept again."); } syncInstallerConsent(); }
function acknowledgeInstallerPolicy() { state.installer.acceptedPolicy = el.installerAck.checked && !state.installer.policyStale ? state.installer.displayedPolicy : null; syncInstallerConsent(); }
function openInstallerDialog() { const policy = state.installer.data?.policy || {}; if (!policy.version) return; setError(el.installerError); presentInstallerPolicy(policy); }
async function startInstaller() {
  syncInstallerConsent(); if (el.installerConfirm.disabled) return;
  const version = state.installer.displayedPolicy.version;
  state.installer.starting = true; syncInstallerConsent(); el.installerConfirm.setAttribute("aria-busy", "true"); setError(el.installerError);
  try { await api("/api/model-installer/install", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ accepted: true, policy_version: version }) }, "installer-start"); el.installerDialog.close(); await loadInstaller(); say("Bundle B installation started."); }
  catch (error) { setError(el.installerError, error.message); if (error.code === "STALE_POLICY" || /policy/i.test(error.message)) { state.installer.policyStale = true; state.installer.acceptedPolicy = null; el.installerAck.checked = false; syncInstallerConsent(); await loadInstaller(); } }
  finally { state.installer.starting = false; el.installerConfirm.removeAttribute("aria-busy"); syncInstallerConsent(); }
}
async function cancelInstaller(operationID, control) { if (!operationID) return; return busy(control, async () => { try { await api("/api/model-installer/cancel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ operation_id: operationID }) }, `installer-cancel-${operationID}`); say("Bundle B cancellation requested."); await loadInstaller(); } catch (error) { setError(el.modelsError, error.message); say(error.message); } }); }
async function loadSystem() {
  if (!el.system.children.length) empty(el.system, "Loading diagnostics…");
  try {
    const data = await api("/api/system", {}, "system");
    el.system.replaceChildren();
    const runtime = data.runtime || {};
    const disk = data.data_free_bytes == null ? NaN : Number(data.data_free_bytes);
    const section = document.createElement("section"); section.className = "app-section";
    const header = document.createElement("div"); header.className = "app-section-header";
    const title = document.createElement("h2"); title.className = "app-section-title"; title.textContent = "Runtime diagnostics"; header.append(title);
    const content = document.createElement("div"); content.className = "app-section-content";
    if (runtime.error) {
      const message = document.createElement("div"); message.className = "diagnostic-message";
      const copy = document.createElement("p"); copy.className = "meta-text"; copy.textContent = runtime.pending ? "Runtime probe is starting…" : runtime.error; message.append(copy);
      if (runtime.detail) {
        const details = document.createElement("details"); details.className = "collapsible"; details.append(disclosureSummary("Probe details"));
        const detail = document.createElement("pre"); detail.className = "collapsible-content probe-detail"; detail.textContent = runtime.detail; details.append(detail); message.append(details);
      }
      content.append(message);
      if (runtime.pending) window.setTimeout(() => { if (location.hash.slice(1) === "system") loadSystem(); }, 2500);
    }
    const values = { Runtime: runtime.device || data.configured_device || "Unavailable", XPU: typeof runtime.xpu_available === "boolean" ? (runtime.xpu_available ? "Available" : "Unavailable") : "Not reported", "Free data space": Number.isFinite(disk) ? `${(disk / 1073741824).toFixed(1)} GiB` : "Unavailable", Queue: data.queue_length ?? "Unavailable", "Device name": runtime.device_name || "Not detected", "PyTorch version": runtime.torch_version || "Unavailable", Version: data.application_version || "Unavailable" };
    const list = document.createElement("dl"); list.className = "system-values";
    Object.entries(values).forEach(([key, value]) => {
      const row = document.createElement("div"); row.className = "system-value";
      const dt = document.createElement("dt"); dt.textContent = key;
      const dd = document.createElement("dd"); dd.textContent = String(value); row.append(dt, dd); list.append(row);
    });
    content.append(list); section.append(header, content); el.system.append(section);
    const compatibility = (Array.isArray(data.compatibility) ? data.compatibility : []).filter((item) => item && typeof item === "object");
    if (compatibility.length) {
      const section = document.createElement("section"); section.className = "app-section";
      const header = document.createElement("div"); header.className = "app-section-header";
      const title = document.createElement("h2"); title.className = "app-section-title"; title.textContent = "Compatibility"; header.append(title);
      const list = document.createElement("ul"); list.className = "app-dense-list";
      compatibility.forEach((item) => {
        const row = document.createElement("li"); row.className = "app-dense-row app-dense-row--plain";
        const copy = document.createElement("div"); copy.className = "app-dense-copy";
        const title = document.createElement("strong"); title.textContent = item.id || "Compatibility";
        const message = document.createElement("p"); message.className = "app-dense-description"; message.textContent = item.message || "This engine is not supported by the current runtime.";
        copy.append(title, message); if (typeof item.reference === "string") appendInstallerLink(copy, "Compatibility details", item.reference);
        row.append(copy); list.append(row);
      });
      section.append(header, list); el.system.append(section);
    }
  } catch (error) { if (error.name !== "AbortError") retryState(el.system, error.message, () => loadSystem()); }
}
function syncCommandShortcutLabel() { const apple = /mac|iphone|ipad|ipod/i.test(globalThis.navigator?.userAgent || ""); const label = document.querySelector("#command-trigger kbd"); if (label) label.textContent = apple ? "⌘K" : "Ctrl K"; }
function route(focusHeading = true) { const view = ["generate", "history", "models", "system"].includes(location.hash.slice(1)) ? location.hash.slice(1) : "generate"; document.title = view === "generate" ? "Uncanny Lab" : `Uncanny Lab — ${view[0].toUpperCase()}${view.slice(1)}`; document.querySelectorAll(".view").forEach((node) => node.hidden = node.id !== view); document.querySelectorAll("[data-view-link]").forEach((node) => { if (node.dataset.viewLink === view) node.setAttribute("aria-current", "page"); else node.removeAttribute("aria-current"); }); if (focusHeading) queueMicrotask(() => document.querySelector(`#${view}-title`)?.focus({ preventScroll: true })); if (view !== "models") stopInstallerPolling(); if (view === "history") loadJobs(); if (view === "models") { loadModels(); loadInstaller(); } if (view === "system") loadSystem(); }
function focusMainFromSkip(event) { event.preventDefault(); document.querySelector("#main-content")?.focus(); }
document.querySelector(".skip-link")?.addEventListener("click", focusMainFromSkip);
el.historyFilter.addEventListener("change", () => { state.historyFilter = el.historyFilter.value; renderHistory(); });
const commandActions = {
  generate: () => { location.hash = "#generate"; }, history: () => { location.hash = "#history"; }, models: () => { location.hash = "#models"; }, system: () => { location.hash = "#system"; },
  reset: () => { if (location.hash !== "#generate") location.hash = "#generate"; queueMicrotask(() => el.reset.click()); }, prompt: () => { if (location.hash !== "#generate") location.hash = "#generate"; queueMicrotask(() => el.form.querySelector('[name="prompt"]')?.focus()); },
};
document.querySelector(".command-palette-list")?.addEventListener("click", (event) => { const action = commandActions[event.target.closest("[data-command-id]")?.dataset.commandId]; if (action) action(); });
el.form.addEventListener("invalid", (event) => { const control = event.target; const group = control.closest('details[data-parameter-group]'); if (group) group.open = true; setError(el.error, control.validationMessage); }, true);
el.form.addEventListener("submit", async (event) => { event.preventDefault(); setError(el.error); if (el.submit.disabled || state.mutations.create) return; if (state.uploads) return setError(el.error, "Wait for image uploads to finish."); const engine = state.engines.find((item) => item.id === new FormData(el.form).get("engine")); if (!ready(engine)) return setError(el.error, "Choose an available engine."); const parameters = {}; const data = new FormData(el.form); Object.entries(engine.parameters || {}).forEach(([name, schema]) => { const value = data.get(name); if (schema.type === "boolean") parameters[name] = data.has(name); else if (value !== null && value !== "") parameters[name] = ["integer", "number"].includes(schema.type) ? Number(value) : value; }); ["source_image", "style_image"].forEach((name) => { const token = inputPreview(name)?.dataset.token; if (token) parameters[name] = token; }); for (const name of engine.required_inputs || []) if (!parameters[name]) return setError(el.error, `${name.replaceAll("_", " ")} is required.`); const body = { engine: engine.id, parameters }; const seed = String(data.get("seed") || "").trim(); if (seed) { if (!Number.isSafeInteger(Number(seed))) { el.form.querySelector('[name="seed"]')?.closest('details').setAttribute('open', ''); el.form.querySelector('[name="seed"]')?.focus(); return setError(el.error, "Seed must be a safe whole number."); } body.seed = Number(seed); } el.submit.disabled = true; el.submit.setAttribute("aria-busy", "true"); try { const job = await api("/api/jobs", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }, "create"); state.selectedPreview = null; state.previewPinned = false; renderCurrent(job); loadJobs(); say("Generation queued."); } catch (error) { setError(el.error, error.message); } finally { el.submit.removeAttribute("aria-busy"); el.submit.disabled = state.uploads > 0 || !ready(engine); } });
el.cancel.addEventListener("click", () => { const job = state.currentJob; if (!job) return; busy(el.cancel, async () => { try { await api(`/api/jobs/${encodeURIComponent(job.id)}/cancel`, { method: "POST" }, `cancel-${job.id}`); say("Cancellation requested."); } catch (error) { setError(el.jobError, error.message); } }); }); el.rerun.addEventListener("click", () => state.currentJob && duplicate(state.currentJob, el.rerun)); $("#confirm-delete").addEventListener("click", (event) => deleteJob(event.currentTarget)); el.installerAck.addEventListener("change", acknowledgeInstallerPolicy); el.installerConfirm.addEventListener("click", startInstaller);
el.downloadFrame.addEventListener("click", () => { if (state.currentJob && state.selectedPreview) location.href = `${artifact(state.currentJob, state.selectedPreview)}?download=1`; }); el.useFrame.addEventListener("click", () => { if (state.currentJob && state.selectedPreview) useAsInput(state.currentJob, state.selectedPreview, el.useFrame); }); el.export.addEventListener("click", () => { if (state.currentJob) location.href = `/api/jobs/${encodeURIComponent(state.currentJob.id)}/export`; }); el.unpin.addEventListener("click", () => { state.previewPinned = false; if (state.currentJob) renderCurrent(state.currentJob); }); el.reset.addEventListener("click", resetGenerationForm); el.zoomOut.addEventListener("click", () => setDetailZoom(state.detailZoom - 0.25)); el.zoomIn.addEventListener("click", () => setDetailZoom(state.detailZoom + 0.25)); el.zoomReset.addEventListener("click", () => setDetailZoom(1));
function resetGenerationForm() { if (state.uploads > 0) return; engineDrafts.delete(renderedEngine); renderedEngine = ""; state.pendingInputs = {}; nextInputRevision("source_image"); nextInputRevision("style_image"); setError(el.error); renderEngineFields(); say("Form reset to defaults."); }
el.confirm.addEventListener("cancel", (event) => { if (state.deletePending) event.preventDefault(); });
el.confirm.addEventListener("close", () => { setDeletePending(false); setError(el.confirmError); });
el.detail.addEventListener("close", () => queueMicrotask(renderHistory));
el.confirm.addEventListener("close", () => queueMicrotask(renderHistory));
function selectMode(mode) { const tab = document.querySelector(`[data-mode="${mode}"]`); if (!tab || state.mode === mode) return; rememberEngineDraft(); renderedEngine = ""; state.mode = mode; document.querySelector("[role=tablist]")?.dispatchEvent(new CustomEvent("tabs:activate", { detail: { id: tab.id } })); renderEngineFields(); const panel = $(mode === "text-to-image" ? "#text-panel" : "#image-panel"); [...panel.children].filter((child) => child !== el.fields && child.classList.contains("empty-note")).forEach((child) => child.remove()); panel.append(el.fields); }
const modeTablist = document.querySelector("[role=tablist]");
modeTablist?.addEventListener("click", (event) => { const tab = event.target.closest("[data-mode]"); if (tab) selectMode(tab.dataset.mode); });
modeTablist?.addEventListener("focusin", (event) => { const tab = event.target.closest("[data-mode]"); if (tab) selectMode(tab.dataset.mode); });
function refreshConnectionStatus() { const { api, stream } = state.connection; if (stream === "error") return api === "ok" ? setConnectionStatus("Reconnecting", "running") : setConnectionStatus("Offline", "negative"); if (api === "error") return setConnectionStatus("Offline", "negative"); if (api === "ok" || stream === "ok") return setConnectionStatus("Connected", "positive"); setConnectionStatus("Connecting", "running"); }
function setConnectionStatus(message, status) { $("#connection-status").textContent = message; $("#connection-status").dataset.state = status; }
function applyWorkerEvent(name, data) { const job = state.currentJob; if (!job) return; const updated = { ...job }; if (name === "progress") { updated.progress_step = Number(data.step ?? updated.progress_step); updated.progress_total = Number(data.total ?? updated.progress_total); } else if (name === "preview") { updated.preview_path = data.path || updated.preview_path; } else if (name === "started") { updated.status = "running"; if (data.device) updated.runtime_device = data.device; } else if (name === "model-loading") { updated.status = "loading-model"; } renderCurrent(updated); }
function connectEventStream() { if (state.stream) return; try { const stream = new EventSource("/api/events"); state.stream = stream; stream.onopen = () => { state.connection.stream = "ok"; refreshConnectionStatus(); loadJobs(true); }; stream.onerror = () => { state.connection.stream = "error"; refreshConnectionStatus(); }; const terminalEvents = new Set(["preparing", "completed", "failed", "cancelled", "saving"]); ["preparing", "started", "model-loading", "progress", "preview", "completed", "failed", "cancelled"].forEach((name) => stream.addEventListener(name, (event) => { try { const envelope = JSON.parse(event.data); if (!envelope) return; if (envelope.job_id !== state.currentJob?.id) { if (terminalEvents.has(name)) loadJobs(!state.currentJob); return; } if (terminalEvents.has(name)) { refreshJob(envelope.job_id); } else { applyWorkerEvent(name, envelope.data || {}); } scheduleJobListRefresh(); } catch { /* Ignore malformed stream data. */ } })); } catch (error) { state.connection.stream = "error"; refreshConnectionStatus(); say(`Live updates unavailable: ${error.message}`); } }
async function init() {
  const attempt = ++state.initAttempt;
  route(false); syncCommandShortcutLabel();
  if (!state.jobs.length) loadJobs(true);
  connectEventStream();
  const engineRequest = api("/api/engines", {}, "init-engines");
  const modelRequest = api("/api/models", {}, "models");
  const modelVersion = state.request.models;
  const [enginesResult, modelsResult] = await Promise.allSettled([engineRequest, modelRequest]);
  if (attempt !== state.initAttempt) return;
  const enginesReady = enginesResult.status === "fulfilled";
  const modelRequestCurrent = state.request.models === modelVersion;
  const modelsReady = modelsResult.status === "fulfilled" && modelRequestCurrent;
  if (enginesReady) {
    state.engines = Array.isArray(enginesResult.value) ? enginesResult.value : enginesResult.value.items || enginesResult.value.engines || [];
    if (state.jobs.length) renderHistory();
  }
  if (modelsReady) {
    setModels(modelsResult.value);
    if (location.hash.slice(1) === "models") loadModels(modelsResult.value);
  } else if (modelRequestCurrent && location.hash.slice(1) === "models") {
    const message = `Unable to load models: ${modelsResult.reason?.message || "request failed"}`;
    setError(el.modelsError, message);
    retryState(el.models, message, () => loadModels());
  }
  if (!enginesReady) {
    showInitError(`Unable to load engines: ${enginesResult.reason?.message || "request failed"}`);
    state.connection.api = "error";
  } else if (!modelsReady && modelRequestCurrent) {
    renderEngineFields();
    showInitError(`Unable to load models: ${modelsResult.reason?.message || "request failed"}`);
    state.connection.api = "ok";
  } else { renderEngineFields(); state.connection.api = "ok"; }
  refreshConnectionStatus();
}
window.addEventListener("hashchange", route); document.addEventListener("visibilitychange", () => { if (document.hidden) { stopInstallerPolling(); window.clearTimeout(state.jobPoll); state.jobPoll = null; } else { if (location.hash.slice(1) === "models") loadInstaller(); loadJobs(); scheduleJobRefresh(); } }); init();
