import { createI18n } from "vue-i18n";
import de from "./de.json";
import en from "./en.json";

const LANG_COOKIE = "ft_lang";

function readCookie(name: string): string | null {
  const m = document.cookie.match(new RegExp("(^|; )" + name + "=([^;]+)"));
  return m ? decodeURIComponent(m[2]) : null;
}

function detectLocale(): "de" | "en" {
  const c = readCookie(LANG_COOKIE);
  if (c === "de" || c === "en") return c;
  const nav = (navigator.language || "de").toLowerCase();
  return nav.startsWith("en") ? "en" : "de";
}

export const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: "de",
  messages: { de, en },
});
