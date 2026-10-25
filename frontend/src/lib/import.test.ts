import test from "node:test";
import assert from "node:assert/strict";
import { parseAccounts } from "./import.ts";

test("delimited and spreadsheet formats preserve password content", () => {
  assert.deepEqual(
    parseAccounts("one@example.com---- pass word ----JBSWY3DPEHPK3PXP")[0],
    {
      email: "one@example.com",
      password: " pass word ",
      totp_secret: "JBSWY3DPEHPK3PXP",
    },
  );
  assert.equal(
    parseAccounts("one@example.com\t---password---\tSECRET")[0].password,
    "---password---",
  );
});
test("CSV supports headers, commas, escaped quotes and embedded newlines", () => {
  const rows = parseAccounts(
    '\uFEFFemail,password,totp_secret\r\none@example.com,"a,b""c\nnext",SECRET',
  );
  assert.equal(rows.length, 1);
  assert.equal(rows[0].password, 'a,b"c\nnext');
});
test("rejects incomplete, duplicate, malformed or oversized imports", () => {
  assert.throws(() => parseAccounts("one@example.com---password"));
  assert.throws(
    () =>
      parseAccounts(
        "one@example.com---pass---SECRET\nONE@example.com---pass---SECRET",
      ),
    /重复/,
  );
  assert.throws(
    () => parseAccounts('one@example.com,"unclosed,SECRET'),
    /闭合/,
  );
  assert.throws(
    () =>
      parseAccounts(
        Array.from(
          { length: 501 },
          (_, i) => `a${i}@example.com---pass---SECRET`,
        ).join("\n"),
      ),
    /500/,
  );
  assert.throws(() => parseAccounts("a".repeat(1024 * 1024 + 1)), /1 MB/);
  assert.deepEqual(parseAccounts("   \n"), []);
});
