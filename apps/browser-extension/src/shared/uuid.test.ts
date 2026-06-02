import { describe, expect, it } from "vitest";
import { createId } from "./uuid";

describe("createId", () => {
  it("creates an RFC 4122 v4-shaped id", () => {
    expect(createId()).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });
});
