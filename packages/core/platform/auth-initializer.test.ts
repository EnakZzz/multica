import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { isInvalidCookieSessionError } from "./auth-initializer";

describe("isInvalidCookieSessionError", () => {
  it("treats unauthorized sessions as invalid cookie sessions", () => {
    expect(isInvalidCookieSessionError(new ApiError("unauthorized", 401, "Unauthorized"))).toBe(true);
  });

  it("treats a missing user behind an otherwise valid JWT as an invalid cookie session", () => {
    expect(isInvalidCookieSessionError(new ApiError("user not found", 404, "Not Found"))).toBe(true);
  });

  it("does not hide unrelated API failures", () => {
    expect(isInvalidCookieSessionError(new ApiError("boom", 500, "Internal Server Error"))).toBe(false);
    expect(isInvalidCookieSessionError(new Error("boom"))).toBe(false);
  });
});
