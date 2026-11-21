import { describe, expect, it } from "vitest";
import { type PauseUser, usersOfOrganization } from "./pauses";

const user = (fields: Partial<PauseUser>): PauseUser => ({
  binding: "",
  kind: "task",
  number: 0,
  repository: "",
  role: "implementer",
  title: "",
  workstream: 0,
  workstreamRepository: "",
  workstreamTitle: "",
  ...fields,
});

const roleUser = user({ kind: "role", role: "curator" });
const userOfA = user({
  repository: "a/site",
  number: 21,
  workstream: 20,
  workstreamRepository: "a/site",
});
const userOfB = user({
  repository: "b/site",
  number: 31,
  workstream: 30,
  workstreamRepository: "b/site",
});

describe("the users of a pause banner of an organization", () => {
  it("lists the global users and the users of the organization", () => {
    expect(usersOfOrganization([roleUser, userOfA, userOfB], "a")).toEqual([roleUser, userOfA]);
  });

  it("lists the users of the Workstream and the global users in a Workstream chat", () => {
    expect(
      usersOfOrganization([roleUser, userOfA, userOfB], "a", {
        repository: "b/site",
        number: 30,
      }),
    ).toEqual([roleUser, userOfB]);
  });

  it("falls back to the users of the organization when no user fits the Workstream", () => {
    expect(
      usersOfOrganization([userOfA, userOfB], "a", { repository: "c/site", number: 1 }),
    ).toEqual([userOfA]);
  });
});
