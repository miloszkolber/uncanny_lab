# Vendored mewa_ui assets

This directory vendors the subset of [mewa_ui](https://github.com/miloszkolber/mewa_ui) that the embedded Uncanny Lab browser UI consumes. mewa_ui is MIT licensed; see `THIRD_PARTY_NOTICES` at the repository root for attribution.

- Source commit: `957485415991fd6b0578b9a5ec752c7f6fceb746` (Prevent mobile app shell navigation overlap).
- Foundations: `library/src/base.css`, `library/src/tokens.css`, Geist font files, and only the Lucide icon files the UI inlines.
- Components: `library/components/{app-shell,alert-dialog,badge,button,card,checkbox,command-palette,dialog,file-input,progress,select,tabs,text-field,textarea}`. Modules load for app-shell, alert-dialog, command-palette, dialog, and tabs.

Do not edit vendored files by hand. Update them by re-copying the listed subset from the recorded upstream commit. Application-specific styles live in `/styles.css` and load after both foundation files.
