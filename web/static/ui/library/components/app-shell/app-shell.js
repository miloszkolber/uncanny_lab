// -- App shell theme enhancement -------------------------------

const APP_SHELL_THEME_KEY = 'mewa-ui-theme';
const APP_SHELL_LEGACY_THEME_KEY = 'mewa-theme';

function readStoredTheme() {
  try {
    const value = window.localStorage.getItem(APP_SHELL_THEME_KEY)
      || window.localStorage.getItem(APP_SHELL_LEGACY_THEME_KEY);
    return value === 'light' || value === 'dark' ? value : null;
  } catch {
    return null;
  }
}

function writeStoredTheme(theme) {
  try {
    window.localStorage.setItem(APP_SHELL_THEME_KEY, theme);
  } catch {
    // Restricted storage still gets an in-page theme change.
  }
}

function preferredTheme() {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function syncToggle(toggle) {
  const dark = document.documentElement.classList.contains('dark');
  toggle.dataset.theme = dark ? 'dark' : 'light';
  toggle.setAttribute('aria-label', dark ? 'Switch to light theme' : 'Switch to dark theme');
}

function applyTheme(theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  document.querySelectorAll('[data-theme-toggle][data-init]').forEach(syncToggle);
}

function init() {
  document.querySelectorAll('[data-theme-toggle]:not([data-init])').forEach((toggle) => {
    toggle.dataset.init = '';
    syncToggle(toggle);
    toggle.addEventListener('click', () => {
      const theme = document.documentElement.classList.contains('dark') ? 'light' : 'dark';
      writeStoredTheme(theme);
      applyTheme(theme);
    });
  });
}

applyTheme(readStoredTheme() || preferredTheme());
init();
new MutationObserver(init).observe(document, { childList: true, subtree: true });

if (!document.__appShellThemeSystemInit) {
  document.__appShellThemeSystemInit = true;
  const colorScheme = window.matchMedia?.('(prefers-color-scheme: dark)');
  colorScheme?.addEventListener('change', (event) => {
    if (!readStoredTheme()) applyTheme(event.matches ? 'dark' : 'light');
  });
}
