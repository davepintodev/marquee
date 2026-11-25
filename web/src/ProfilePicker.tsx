import type { Profiles, TitleSummary } from "./api/types";
import { IconChevron } from "./icons";
import "./profile.css";

export const defaultProfile = (profiles: Profiles, kind: TitleSummary["kind"]) => (kind === "movie" ? profiles.defaultMovieProfile : profiles.defaultSeriesProfile);

interface Props {
  profiles: Profiles;
  value: string;
  onChange: (name: string) => void;
}

export function ProfilePicker({ profiles, value, onChange }: Props) {
  return (
    <label className="pp">
      <span className="pp-label">Profile</span>
      <select className="pp-select" aria-label="Quality profile" value={value} onChange={(e) => onChange(e.target.value)}>
        {profiles.profiles.map((p) => (
          <option key={p.name} value={p.name}>
            {p.name}
          </option>
        ))}
      </select>
      <IconChevron open={false} />
    </label>
  );
}
