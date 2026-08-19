# playwright-go-stealth vendored asset

`stealth.min.js` is vendored from
[`jonfriesen/playwright-go-stealth`](https://github.com/jonfriesen/playwright-go-stealth)
at commit `b2483b64ee2ae3578a92129457b1c1fc406d324e`.

- Upstream asset date: 2025-10-11
- `stealth.min.js` SHA-256: `375dd3a300f31a6e95a429e16ba1920dc2b7645a454662e851e74ab1f157a557`
- `chrome_stealth.js` SHA-256: `573ab09fc19fd780498c697e290c8a6c769f517a6fae8f19ce0417d913564f70`
- License: MIT; see `LICENSE`

The JavaScript is embedded directly because upstream imports the old
`github.com/playwright-community/playwright-go` module, whose page types are
not compatible with this project's `github.com/mxschmitt/playwright-go`
runtime.

The upstream `chrome_stealth.js` is also vendored and is mandatory for every
browser context. It runs directly after the base bundle and suppresses console
output, maps permission `prompt` results to `denied`, wraps DOM events to spoof
`isTrusted`, and automatically clicks Cloudflare challenge controls.

Cineko replaces the bundle's default `en-US` language and Intel WebGL
arguments at runtime with values read from the unmodified Chrome profile and
GPU before installing the init script. UA and UA-CH coherence is handled by
the adjacent Go adapter rather than by this upstream asset.
