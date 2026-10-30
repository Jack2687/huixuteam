import test from "node:test";
import assert from "node:assert/strict";
import { parseVerificationStatuses } from "./admin-verification-statuses.ts";

test("verification status editor preserves explicit empty and comma separated values", () => {
  assert.deepEqual(parseVerificationStatuses("404, 409，429"), [404, 409, 429]);
  assert.deepEqual(parseVerificationStatuses(" "), []);
  for (const value of ["200", "600", "404,", "404,,409", "404 409", "404,404", "404.5", "abc"]) {
    assert.throws(() => parseVerificationStatuses(value));
  }
});
