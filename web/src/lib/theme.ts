// Themes and accent colours are CSS variables on <html>, so switching needs
// no rebuild: the light palette lives in index.css under html.light, and an
// accent sets the four accent variables from one colour.

export type Theme = "dark" | "light" | "system";

type RGB = [number, number, number];

const hex = (c: string): RGB | null => {
  const m = /^#([0-9a-f]{6})$/i.exec(c.trim());
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
};
const toHex = (c: RGB) => "#" + c.map((v) => Math.round(Math.min(255, Math.max(0, v))).toString(16).padStart(2, "0")).join("");
const mix = (a: RGB, b: RGB, t: number): RGB => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t];
const BLACK: RGB = [0, 0, 0];
const WHITE: RGB = [255, 255, 255];

/** luminance is the WCAG relative luminance. */
function luminance([r, g, b]: RGB): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

/** contrast is the WCAG contrast ratio of two colours. */
export function contrast(a: string, b: string): number {
  const x = hex(a);
  const y = hex(b);
  if (!x || !y) return 1;
  const [l1, l2] = [luminance(x), luminance(y)].sort((p, q) => q - p);
  return (l1 + 0.05) / (l2 + 0.05);
}

/** towards moves c towards target until it has ratio against against. */
function towards(c: RGB, target: RGB, against: string, ratio: number): RGB {
  for (let t = 0; t <= 1; t += 0.04) {
    const v = mix(c, target, t);
    if (contrast(toHex(v), against) >= ratio) return v;
  }
  return target;
}

/**
 * accentVars derives the accent variables from one colour: the accent as is,
 * a text shade that reads on the page background, and button fills dark
 * enough for white text (4.5:1).
 */
export function accentVars(accent: string, mode: "dark" | "light"): Record<string, string> | null {
  const c = hex(accent);
  if (!c) return null;
  const page = mode === "dark" ? "#0f1115" : "#f6f7f9";
  const text = towards(c, mode === "dark" ? WHITE : BLACK, page, 4.5);
  const primary = towards(c, BLACK, "#ffffff", 4.5);
  return {
    "--color-accent": toHex(c),
    "--color-accent-2": toHex(text),
    "--color-primary": toHex(primary),
    "--color-primary-hover": toHex(mix(primary, BLACK, 0.12)),
  };
}

const vars = ["--color-accent", "--color-accent-2", "--color-primary", "--color-primary-hover"];

/** resolveTheme turns "system" into what the device prefers. */
export function resolveTheme(theme: Theme): "dark" | "light" {
  if (theme !== "system") return theme;
  return typeof matchMedia !== "undefined" && matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/** applyAppearance puts a theme and an accent on the page. */
export function applyAppearance(theme: Theme, accent: string) {
  const root = document.documentElement;
  const mode = resolveTheme(theme);
  root.classList.toggle("dark", mode === "dark");
  root.classList.toggle("light", mode === "light");
  root.style.colorScheme = mode;
  const set = accent ? accentVars(accent, mode) : null;
  for (const v of vars) {
    if (set) root.style.setProperty(v, set[v]);
    else root.style.removeProperty(v);
  }
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", mode === "dark" ? "#0f1115" : "#f6f7f9");
}
