// This module is loaded only in Cineko's isolated booking browser profile.
// It does not inspect websites, send requests, or request extension permissions.
let startup;

async function ensureWindow() {
  const windows = await chrome.windows.getAll({ windowTypes: ["normal"] });
  if (windows.length > 1) throw new Error("Expected one Cineko booking window");
  if (windows.length === 1) return windows[0].id;
  const window = await chrome.windows.create({
    url: "about:blank",
    focused: false,
    state: "minimized",
    type: "normal",
  });
  if (!window?.id) throw new Error("Cineko booking window was not created");
  return window.id;
}

function initialize() {
  startup ??= ensureWindow().catch((error) => {
    startup = undefined;
    throw error;
  });
  return startup;
}

chrome.runtime.onInstalled.addListener(() => { void initialize(); });
chrome.runtime.onStartup.addListener(() => { void initialize(); });

globalThis.cinekoWindowControl = {
  initialize,
  async createTab({ windowId, marker }) {
    if (!Number.isInteger(windowId) || !/^about:blank#cineko-tab-[a-zA-Z0-9]+$/.test(marker)) {
      throw new Error("Invalid Cineko background tab request");
    }
    // Never recreate a closed window or select an arbitrary user's window.
    await chrome.windows.get(windowId);
    const tab = await chrome.tabs.create({ windowId, url: marker, active: false });
    return tab.id;
  },
  async closeTab(tabId) {
    await chrome.tabs.remove(tabId);
  },
};
