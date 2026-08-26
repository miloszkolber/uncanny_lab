// -- Alert Dialog ----------------------------------------------

function openAlertDialog(dialog, trigger) {
  if (!dialog || !dialog.isConnected || typeof dialog.showModal !== 'function') return;
  if (dialog.open) return;
  dialog._trigger = trigger;
  try {
    dialog.showModal();
  } catch {
    // Do not surface a native InvalidStateError during SPA replacement.
  }
}

function init() {
  document.querySelectorAll('[data-alert-dialog-trigger]:not([data-init])').forEach((trigger) => {
    trigger.dataset.init = '';
    const dialogId = trigger.dataset.alertDialogTrigger;
    if (!document.getElementById(dialogId)) {
      delete trigger.dataset.init;
      return;
    }
    trigger.addEventListener('click', () => {
      openAlertDialog(document.getElementById(dialogId), trigger);
    });
  });

  document.querySelectorAll('dialog.alert-dialog:not([data-init])').forEach((dialog) => {
    dialog.dataset.init = '';

    dialog.querySelectorAll('[data-alert-dialog-close]').forEach((button) => {
      button.addEventListener('click', () => {
        dialog.close();
      });
    });

    dialog.addEventListener('close', () => {
      if (dialog._trigger?.isConnected) dialog._trigger.focus();
    });
  });
}

init();
new MutationObserver(init).observe(document, { childList: true, subtree: true });
