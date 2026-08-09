// Theme preference: "auto" follows the OS, "light"/"dark" pin it. Stored per
// browser (localStorage) — a display choice, not server state. index.html
// applies the saved value before the bundle loads (no first-paint flash);
// this module owns changes after that, including live OS-theme switches
// while in "auto".

export type ThemePref = "auto" | "light" | "dark";

const KEY = "librinode-theme";
const media = window.matchMedia("(prefers-color-scheme: light)");

export function getThemePref(): ThemePref {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    /* no localStorage — auto */
  }
  return "auto";
}

export function setThemePref(pref: ThemePref) {
  try {
    if (pref === "auto") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, pref);
  } catch {
    /* not persistable — still applies for this page */
  }
  applyTheme(pref);
}

function applyTheme(pref: ThemePref) {
  const light = pref === "light" || (pref === "auto" && media.matches);
  if (light) {
    document.documentElement.dataset.theme = "light";
  } else {
    delete document.documentElement.dataset.theme;
  }
}

// In "auto", follow the OS live.
media.addEventListener("change", () => applyTheme(getThemePref()));
