const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

function fixture(existing = []) {
  const calls = [];
  const events = {};
  const chrome = {
    runtime: {
      onInstalled: { addListener: (callback) => { events.installed = callback; } },
      onStartup: { addListener: (callback) => { events.startup = callback; } },
    },
    windows: {
      getAll: async () => existing,
      create: async (options) => { calls.push(["window", options]); return { id: 11 }; },
      get: async (id) => ({ id }),
    },
    tabs: {
      create: async (options) => { calls.push(["tab", options]); return { id: 12 }; },
      remove: async (id) => { calls.push(["close", id]); },
    },
  };
  const context = vm.createContext({ chrome });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "window_control/background.js"), "utf8"), context);
  return { control: context.cinekoWindowControl, chrome, events, calls };
}

test("startup events share one inactive minimized window creation", async () => {
  const { control, events, calls } = fixture();
  events.installed();
  events.startup();
  assert.equal(await control.initialize(), 11);
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1].focused, false);
  assert.equal(calls[0][1].state, "minimized");
  assert.equal(calls[0][1].url, "about:blank");
});

test("reuse the existing owned window; create only inactive tabs", async () => {
  const { control, calls } = fixture([{ id: 7 }]);
  assert.equal(await control.initialize(), 7);
  const marker = "about:blank#cineko-tab-abc123";
  assert.equal(await control.createTab({ windowId: 7, marker }), 12);
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], "tab");
  assert.equal(calls[0][1].windowId, 7);
  assert.equal(calls[0][1].active, false);
  assert.equal(calls[0][1].url, marker);
  await control.closeTab(12);
  assert.deepEqual(calls[1], ["close", 12]);
});

test("invalid markers and missing windows do not create replacements", async () => {
  const { control, chrome, calls } = fixture();
  for (const marker of ["https://example.invalid", "about:blank", "about:blank#cineko-tab-../"]) {
    await assert.rejects(control.createTab({ windowId: 7, marker }), /Invalid/);
  }
  chrome.windows.get = async () => { throw new Error("window closed"); };
  await assert.rejects(control.createTab({ windowId: 7, marker: "about:blank#cineko-tab-abc" }), /window closed/);
  assert.equal(calls.length, 0);
});

test("ambiguous window ownership fails closed", async () => {
  const { control, calls } = fixture([{ id: 7 }, { id: 8 }]);
  await assert.rejects(control.initialize(), /Expected one/);
  assert.equal(calls.length, 0);
});
