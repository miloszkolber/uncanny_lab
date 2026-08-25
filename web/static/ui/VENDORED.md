# Vendored mewa_ui assets

This directory vendors the subset of [mewa_ui](https://github.com/miloszkolber/ui_library) that the embedded Uncanny Lab browser UI consumes. mewa_ui is MIT licensed; see `THIRD_PARTY_NOTICES` at the repository root for attribution.

- Source commit: `4d7bdb6` (feat: add tabs:activate programmatic activation event)
- Foundations: `src/base.css`, `src/tokens.css`, Geist font files, and only the Lucide icon files the UI inlines.
- Components: only the stylesheets listed in `index.html` (app-shell, button, badge, card, checkbox, collapsible, command-palette, dialog, file-input, label, progress, select, tabs, text-field, textarea). The app-shell module backs the optional theme toggle.

Do not edit vendored files by hand. Update them by re-copying from an upstream checkout of the same or a newer commit. Application-specific styles live in `/styles.css` and load after both foundation files.
