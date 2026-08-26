// -- Tabs -----------------------------------------------------

const selectTab = (tab, triggers) => {
  triggers.forEach((t) => {
    t.setAttribute('aria-selected', 'false');
    t.setAttribute('tabindex', '-1');
    const panel = document.getElementById(t.getAttribute('aria-controls'));
    if (panel) panel.hidden = true;
  });
  tab.setAttribute('aria-selected', 'true');
  tab.removeAttribute('tabindex');
  const panel = document.getElementById(tab.getAttribute('aria-controls'));
  if (panel) panel.hidden = false;
};

// Activate a tab inside one tablist. `target` is a tab element, a tab id,
// or the id of the panel a tab controls. Disabled tabs are ignored.
const activateInTablist = (tablist, target) => {
  const triggers = Array.from(tablist.querySelectorAll('[role="tab"]'));
  let tab = null;
  if (typeof target === 'string') {
    tab = triggers.find((t) => t.id === target)
      || triggers.find((t) => t.getAttribute('aria-controls') === target);
  } else if (target && triggers.includes(target)) {
    tab = target;
  }
  if (!tab || tab.disabled) return false;
  selectTab(tab, triggers);
  return true;
};

function init() {
document.querySelectorAll('[role="tablist"]:not([data-init])').forEach((tablist) => {
    tablist.dataset.init = '';
    if (!tablist.querySelector('.tab-trigger')) return;
    const triggers = Array.from(tablist.querySelectorAll('[role="tab"]'));
    const orientation = tablist.getAttribute('aria-orientation') || 'horizontal';

    // Programmatic activation for application code. Dispatch `tabs:activate`
    // on the tablist with `{ detail: { id } }` to switch tabs without a click.
    tablist.addEventListener('tabs:activate', (event) => {
      if (event.detail) activateInTablist(tablist, event.detail.id);
    });

    triggers.forEach((trigger) => {
      trigger.addEventListener('click', () => { selectTab(trigger, triggers); });
      trigger.addEventListener('keydown', (e) => {
        const current = triggers.indexOf(trigger);
        let next;
        const forward = orientation === 'horizontal' ? 'ArrowRight' : 'ArrowDown';
        const backward = orientation === 'horizontal' ? 'ArrowLeft' : 'ArrowUp';
        switch (e.key) {
          case forward:
            e.preventDefault();
            for (let i = 1; i <= triggers.length; i++) {
              const c = triggers[(current + i) % triggers.length];
              if (!c.disabled) { next = c; break; }
            }
            break;
          case backward:
            e.preventDefault();
            for (let i = 1; i <= triggers.length; i++) {
              const c = triggers[(current - i + triggers.length) % triggers.length];
              if (!c.disabled) { next = c; break; }
            }
            break;
          case 'Home':
            e.preventDefault();
            next = triggers.find((t) => !t.disabled);
            break;
          case 'End':
            e.preventDefault();
            next = triggers.slice().reverse().find((t) => !t.disabled);
            break;
        }
        if (next && !next.disabled) {
          selectTab(next, triggers);
          next.focus();
        }
      });
    });
});}

init();
new MutationObserver(init).observe(document, { childList: true, subtree: true });
