# Meshway local browser proxy

This extension only configures the browser to use a local Meshway SOCKS5 proxy. It does not start the daemon or provide a device-wide VPN. Start and configure a trusted upstream peer in the daemon before enabling it.

From the repository root:

```sh
npm install
npm run build --workspace @meshway/extension
npm run test --workspace @meshway/extension
```

Chrome / Chromium: open `chrome://extensions`, enable developer mode, and load `apps/extension/dist/chrome` unpacked. Firefox: open `about:debugging#/runtime/this-firefox`, choose Load Temporary Add-on, and select `apps/extension/dist/firefox/manifest.json`. Firefox temporary installation ends when the browser closes. Isolated Chromium runtime enable/restore tests pass; Firefox runtime and store publication/signing remain release gates.

The popup requires a loopback SOCKS endpoint and explicit consent. Previous proxy ownership/settings are persisted before changes so Chromium's service worker can unload safely. Disabling clears Meshway's override and reveals the browser's inherited settings. If Meshway owned a setting before enable, that exact setting is restored. Browser policy and other extensions are respected. Uninstalling the extension removes its settings override through the browser's own extension lifecycle.

Permissions: `proxy` configures the browser proxy. `storage` retains local recovery settings across service-worker restarts. There are no host permissions, content scripts, tabs/history permissions, or analytics. No credentials or browsing data are stored. The extension makes no HTTP requests and can only configure a loopback proxy.

Only TCP SOCKS5 traffic is supported by the core. Firefox enables SOCKS DNS. Chromium does not expose an equivalent extension DNS switch. The browser's WebRTC, DNS, UDP, local bypasses, and application traffic outside the browser are not a complete VPN privacy boundary. Browser pages may fail when the daemon is unavailable; disable the proxy to restore access. Incognito/private windows are not configured by this extension.
