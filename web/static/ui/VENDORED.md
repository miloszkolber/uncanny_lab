# Vendored mewa_ui assets

This directory vendors the subset of [mewa_ui](https://github.com/miloszkolber/ui_library) that the embedded Uncanny Lab browser UI consumes. mewa_ui is MIT licensed; see `THIRD_PARTY_NOTICES` at the repository root for attribution.

- Source commit: `80bf66187607ce964f71d641b421f7b33f75e3a6`
- Foundations: `src/base.css`, `src/tokens.css`, Geist font files, and only the Lucide icon files the UI inlines.
- Components: only the stylesheets listed in `index.html` (button, badge, card, checkbox, collapsible, dialog, file-input, label, progress, select, tabs, text-field, textarea).

Do not edit vendored files by hand. Update them by re-copying from an upstream checkout of the same or a newer commit. Application-specific styles live in `/styles.css` and load after both foundation files.
