import { expect, test } from "vitest";
import { stateLabel, waitLine } from "./words";

const now = new Date("2026-11-18T12:00:00Z");

test("a wanted item says when it is searched next, and shortly once that is due", () => {
  expect(waitLine("wanted", "2026-11-18T14:00:00Z", now)).toBe("Next search in 2h");
  expect(waitLine("wanted", "2026-11-18T12:20:00Z", now)).toBe("Next search in 20m");
  expect(waitLine("wanted", "2026-11-18T12:00:00Z", now)).toBe("Next search shortly");
  expect(waitLine("wanted", null, now)).toBeNull();
});

test("an item whose search found nothing says so with the time of the next try", () => {
  expect(waitLine("not_found", "2026-11-18T18:00:00Z", now)).toBe("Nothing found · next try in 6h");
  expect(waitLine("not_found", "2026-11-18T11:00:00Z", now)).toBe("Nothing found · trying again shortly");
  expect(waitLine("not_found", undefined, now)).toBe("Nothing found");
});

test("a running search has no time, and every state has its word", () => {
  expect(waitLine("searching", "2026-11-18T18:00:00Z", now)).toBe("Searching now");
  expect([stateLabel({ state: "wanted" }), stateLabel({ state: "searching" }), stateLabel({ state: "not_found" })]).toEqual(["Wanted", "Searching", "Not found"]);
});
