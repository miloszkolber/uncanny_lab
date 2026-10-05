(function () {
    "use strict";
    var stored = null;
    try {
        stored = localStorage.getItem("mewa-ui-theme");
        if (stored !== "light" && stored !== "dark") stored = localStorage.getItem("mewa-theme");
    } catch (error) {}
    var theme = stored === "light" || stored === "dark"
        ? stored
        : (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
}());
