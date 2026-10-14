# Meshway local browser proxy

This extension only configures the browser to use a local Meshway SOCKS5 proxy. It does not start the daemon or provide a device-wide VPN. Start and configure a trusted upstream peer in the daemon before enabling it.

From the repository root:

```sh
npm install
npm run build --workspace @meshway/extension
npm run test --workspace @meshway/extension
```

Chrome / Chromium: open `chrome://extensions`, enable developer mode, and load `apps/extension/dist/chrome` unpacked. Firefox: open `about:debugging#/runtime/this-firefox`, choose Load Temporary Add-on, and select `apps/extension/dist/firefox/manifest.json`. Firefox temporary installation ends when the browser closes.

Firefox requires permission to run in private windows before an extension can change browser-wide proxy settings. Open `about:addons`, select Meshway, and set **Run in Private Windows** to **Allow**, then enable the proxy with the popup's consent checkbox. Without this permission, Firefox reports `proxy.settings requires private browsing permission.` and Meshway leaves the previous settings unchanged. Firefox's proxy setting also applies to private windows; Chromium's setting applies to the regular browser profile.

Isolated Chromium and Firefox runtime enable/restore checks pass. The [Firefox evidence](../../artifacts/firefox-extension-validation.json) records real SOCKS5 browser traffic, DNS-through-SOCKS framing, loopback bypass, exact restoration of a prior proxy and cleanup. Reproduce with `npm exec playwright install firefox` and `node scripts/firefox-extension-lab.mjs` from the repository root; set `FIREFOX_EXECUTABLE` to an existing Playwright Firefox executable if needed. Store publication/signing and field use remain release gates.

The popup requires a loopback SOCKS endpoint and explicit consent. Previous proxy ownership/settings are persisted before changes so Chromium's service worker can unload safely. Disabling clears Meshway's override and reveals the browser's inherited settings. If Meshway owned a setting before enable, that exact setting is restored. Browser policy and other extensions are respected. Uninstalling the extension removes its settings override through the browser's own extension lifecycle.

Permissions: `proxy` configures the browser proxy. `storage` retains local recovery settings across service-worker restarts. There are no host permissions, content scripts, tabs/history permissions, or analytics. No credentials or browsing data are stored. The extension makes no HTTP requests and can only configure a loopback proxy.

This extension configures browser TCP traffic through SOCKS5. Firefox enables SOCKS DNS. Chromium does not expose an equivalent extension DNS switch. The browser's WebRTC, DNS, UDP, local bypasses, and application traffic outside the browser are not a complete VPN privacy boundary. Browser pages may fail when the daemon is unavailable; disable the proxy to restore access.
