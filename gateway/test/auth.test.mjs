import assert from "node:assert/strict";
import test from "node:test";
import { createCaptchaStore, isAuthorized, validateCredentials } from "../src/auth.mjs";

test("captcha challenges verify once and reject replay", () => {
  const store = createCaptchaStore();
  const challenge = store.createChallenge();
  const answer = challenge.expression.match(/(\d+)\s*([+-])\s*(\d+)/);
  const value = answer[2] === "+" ? Number(answer[1]) + Number(answer[3]) : Number(answer[1]) - Number(answer[3]);
  assert.equal(store.verify(challenge.captchaId, String(value)), true);
  assert.equal(store.verify(challenge.captchaId, String(value)), false, "a challenge must be single-use");
  assert.equal(store.verify("missing", "1"), false);
});

test("credentials and bearer tokens are compared safely", () => {
  const config = { authUsername: "admin", authPassword: "pw", authToken: "tok" };
  assert.equal(validateCredentials(config, "admin", "pw"), true);
  assert.equal(validateCredentials(config, "admin", "pw "), false);
  assert.equal(validateCredentials(config, "Admin", "pw"), false);

  const withHeader = { headers: { authorization: "Bearer tok" }, url: "/sessions" };
  const withQuery = { headers: {}, url: "/sessions?token=tok" };
  const withNothing = { headers: {}, url: "/sessions" };
  assert.equal(isAuthorized(withHeader, config), true);
  assert.equal(isAuthorized(withQuery, config), true);
  assert.equal(isAuthorized(withNothing, config), false);
});
