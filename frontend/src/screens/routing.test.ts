import { describe, expect, it } from "vitest";
import { APP_ROUTES } from "./AppShell";

describe("client-side router scope (Guardrail #2 / requirement #7)", () => {
  it("no route pattern claims any /api/* path", () => {
    for (const route of APP_ROUTES) {
      expect(route.startsWith("/api")).toBe(false);
    }
  });
});
