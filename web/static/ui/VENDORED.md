# Vendored mewa_ui assets

This directory vendors the subset of [mewa_ui](https://github.com/miloszkolber/mewa_ui) that the embedded Uncanny Lab browser UI consumes. mewa_ui is MIT licensed; see `THIRD_PARTY_NOTICES` at the repository root for attribution.

- Source commit: `e475c72e462fc3a520ba80ed5d4448749f05ef9d` (Clean up).
- Foundations: `library/src/base.css`, `library/src/tokens.css`, Geist font files, and only the Lucide icon files the UI inlines.
- Components: `library/components/{app-shell,alert-dialog,badge,button,card,checkbox,command-palette,dialog,file-input,progress,select,tabs,text-field,textarea}`. Modules load for app-shell, alert-dialog, command-palette, dialog, and tabs.

Do not edit vendored files by hand. Update them by re-copying the listed subset from the recorded upstream commit. Application-specific styles live in `/styles.css` and load after both foundation files.
