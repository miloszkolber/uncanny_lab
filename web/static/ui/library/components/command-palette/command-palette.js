// -- Command Palette -----------------------------------------

let commandItemId = 0;

function isConnected(element) {
  return Boolean(element && element.isConnected);
}

function isDisabled(item) {
  return item.disabled || item.getAttribute('aria-disabled') === 'true';
}

function getVisibleItems(list) {
  return Array.from(list.querySelectorAll('.command-palette-item'))
    .filter((item) => !item.hidden && !isDisabled(item));
}

function clearHighlight(list, input) {
  list.querySelectorAll('.command-palette-item').forEach((item) => {
    item.removeAttribute('data-highlighted');
    item.setAttribute('aria-selected', 'false');
  });
  if (input) input.removeAttribute('aria-activedescendant');
}

function highlightItem(list, index, input) {
  const visible = getVisibleItems(list);
  clearHighlight(list, input);
  if (visible.length === 0) return -1;

  const clamped = ((index % visible.length) + visible.length) % visible.length;
  const item = visible[clamped];
  item.setAttribute('data-highlighted', '');
  item.setAttribute('aria-selected', 'true');
  if (input && item.id) input.setAttribute('aria-activedescendant', item.id);
  if (typeof item.scrollIntoView === 'function') item.scrollIntoView({ block: 'nearest' });
  return clamped;
}

function showPalette(dialog, trigger) {
  if (!isConnected(dialog) || typeof dialog.showModal !== 'function') return false;
  if (!dialog.open) {
    if (trigger) dialog._trigger = trigger;
    try {
      dialog.showModal();
    } catch {
      return false;
    }
  }
  const input = dialog.querySelector('.command-palette-input');
  if (input) input.focus();
  return true;
}

if (!document.__commandPaletteKeydownInit) {
  document.__commandPaletteKeydownInit = true;
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      const dialog = document.querySelector('dialog.command-palette');
      if (!isConnected(dialog)) return;
      e.preventDefault();
      if (dialog.open) dialog.close();
      else showPalette(dialog);
    }
  });
}

function init() {
  document.querySelectorAll('dialog.command-palette:not([data-init])').forEach((dialog) => {
    dialog.dataset.init = '';
    const input = dialog.querySelector('.command-palette-input');
    const inputWrapper = dialog.querySelector('.command-palette-input-wrapper');
    const list = dialog.querySelector('.command-palette-list');
    const empty = dialog.querySelector('.command-palette-empty');
    if (!input || !list) {
      delete dialog.dataset.init;
      return;
    }

    if (!list.id) list.setAttribute('id', `command-palette-list-${++commandItemId}`);
    input.setAttribute('aria-controls', list.id);

    const items = Array.from(list.querySelectorAll('.command-palette-item'));
    items.forEach((item, index) => {
      if (!item.id) item.setAttribute('id', `${list.id}-item-${index + 1}`);
      item.setAttribute('aria-selected', 'false');
      item.addEventListener('click', (event) => {
        if (!isDisabled(item)) return;
        event.preventDefault();
        if (typeof event.stopImmediatePropagation === 'function') event.stopImmediatePropagation();
        else event.stopPropagation();
      });
    });
    let highlightIndex = -1;

    const filter = (q) => {
      const query = q.toLowerCase();
      items.forEach((item) => {
        item.hidden = Boolean(query) && !item.textContent.toLowerCase().includes(query);
      });
      list.querySelectorAll('.command-palette-group').forEach((group) => {
        group.hidden = !Array.from(group.querySelectorAll('.command-palette-item')).some((item) => !item.hidden);
      });
      list.querySelectorAll('.command-palette-separator').forEach((separator) => {
        separator.hidden = Boolean(query);
      });
      if (empty) empty.hidden = items.some((item) => !item.hidden);
      highlightIndex = highlightItem(list, 0, input);
    };

    input.addEventListener('input', () => { filter(input.value); });
    if (inputWrapper) inputWrapper.addEventListener('click', () => { input.focus(); });

    input.addEventListener('keydown', (e) => {
      const visible = getVisibleItems(list);
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        highlightIndex = highlightItem(list, highlightIndex + 1, input);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        highlightIndex = highlightItem(list, highlightIndex - 1, input);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const item = visible[highlightIndex];
        if (item && !isDisabled(item)) item.click();
      } else if (e.key === 'Home') {
        e.preventDefault();
        highlightIndex = highlightItem(list, 0, input);
      } else if (e.key === 'End') {
        e.preventDefault();
        highlightIndex = highlightItem(list, visible.length - 1, input);
      }
    });

    dialog.addEventListener('click', (e) => {
      if (e.target === dialog) {
        if (dialog.open) dialog.close();
        return;
      }
      const item = e.target.closest('.command-palette-item');
      if (!item) return;
      if (isDisabled(item)) {
        e.preventDefault();
        return;
      }
      if (dialog.open) dialog.close();
    });
    dialog.addEventListener('close', () => {
      input.value = '';
      filter('');
      clearHighlight(list, input);
      highlightIndex = -1;
      if (dialog._trigger?.isConnected) dialog._trigger.focus();
    });

    filter('');
  });

  document.querySelectorAll('[data-command-palette-trigger]:not([data-init])').forEach((trigger) => {
    trigger.dataset.init = '';
    const dialogId = trigger.dataset.commandPaletteTrigger;
    const dialog = document.getElementById(dialogId);
    if (!dialog) {
      // Leave the trigger eligible for a later SPA insertion of its dialog.
      delete trigger.dataset.init;
      return;
    }
    trigger.addEventListener('click', () => {
      const currentDialog = document.getElementById(dialogId);
      showPalette(currentDialog, trigger);
    });
  });
}

init();
new MutationObserver(init).observe(document, { childList: true, subtree: true });
