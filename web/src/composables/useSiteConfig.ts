import { ref, type Ref } from "vue";
import { api, type SiteConfig } from "../api";

const config: Ref<SiteConfig | null> = ref(null);
const error: Ref<string | null> = ref(null);
let inflight: Promise<SiteConfig> | null = null;

export function useSiteConfig() {
  if (!config.value && !inflight) {
    inflight = api
      .siteConfig()
      .then((c) => {
        config.value = c;
        return c;
      })
      .catch((e) => {
        error.value = String(e);
        throw e;
      });
  }
  return { config, error, ready: inflight as Promise<SiteConfig> };
}
