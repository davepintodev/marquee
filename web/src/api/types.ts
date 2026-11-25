import type { components } from "./schema";

type Schemas = components["schemas"];

export type Badge = Schemas["Badge"];
export type BadgeState = Schemas["BadgeState"];
export type TitleSummary = Schemas["TitleSummary"];
export type Title = Schemas["Title"];
export type MonitorChoice = Schemas["MonitorChoice"];
export type Wall = Schemas["Wall"];
export type TitleAbout = Schemas["TitleAbout"];
export type Rating = Schemas["Rating"];
export type CastMember = Schemas["CastMember"];
export type Release = Schemas["Release"];
export type Profiles = Schemas["Profiles"];
export type Releases = Schemas["Releases"];
export type Download = Schemas["Download"];
export type HistoryEntry = Schemas["HistoryEntry"];
export type ProgressEvent = Schemas["ProgressEvent"];
export type Health = Schemas["Health"];
export type Episode = Schemas["Episode"];
export type Season = Schemas["Season"];
export type FailureReason = Schemas["FailureReason"];
export type TitleEvent = Schemas["TitleEvent"];

export type LiveEvent =
  | { type: "progress"; data: ProgressEvent }
  | { type: "title"; data: TitleEvent }
  // The stream dropped and came back; events in between were lost.
  | { type: "reconnected" };
