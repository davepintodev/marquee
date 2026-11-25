import { useEffect, useState } from "react";
import type { Api } from "./api/client";
import type { Profiles } from "./api/types";

// The picker is an extra: when the profiles cannot be had, the screen works without it.
export function useProfiles(api: Api): Profiles | null {
  const [profiles, setProfiles] = useState<Profiles | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    api.profiles(ctl.signal).then(
      (p) => !ctl.signal.aborted && setProfiles(p),
      () => {},
    );
    return () => ctl.abort();
  }, [api]);
  return profiles;
}
