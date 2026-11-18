const KEY = "marquee.token";

// The API token lives in the browser so each device is unlocked once. A
// browser that blocks storage (a private window, cleared site data) still
// works for the visit: the token then lives in memory only.
export function tokenStore() {
  let memory: string | null = null;
  return {
    get(): string | null {
      try {
        const stored = localStorage.getItem(KEY);
        if (stored) return stored;
      } catch {
        /* storage is blocked */
      }
      return memory;
    },
    set(token: string) {
      memory = token;
      try {
        localStorage.setItem(KEY, token);
      } catch {
        /* storage is blocked */
      }
    },
    clear() {
      memory = null;
      try {
        localStorage.removeItem(KEY);
      } catch {
        /* storage is blocked */
      }
    },
  };
}
