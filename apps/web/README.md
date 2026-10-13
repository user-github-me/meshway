# Meshway website and local dashboard

TypeScript + React + Vite. The public site includes installation instructions, a truthful capability matrix, explanatory role selector, and embedded development documentation. `#/dashboard` uses the real loopback daemon API, never simulated data.

```sh
npm install
npm run dev
npm run build --workspace @meshway/web
npm run test --workspace @meshway/web
```

Run `meshway daemon --config meshway.json` separately. Open `http://127.0.0.1:5173/#/dashboard`, enter the loopback API address and `api_token` from your local configuration. Tokens live in page memory, are never written to storage, and disappear on disconnect/reload. Requests omit cookies and reject redirects. Only literal localhost, 127.0.0.1, or ::1 HTTP origins can receive a token. Add the exact UI origin to the daemon's `allowed_origins` if your dev server uses another port. See `docs/PROTOCOL.md`.

The dashboard leads with actual advertised server choices in list/map views, connecting an authorized exit through the real selection API. Map markers require an explicitly shared country code; there are no sample servers or inferred locations. Advanced details expose peer identities/addresses, actual forwarding counters and roles, and multiaddress connection controls. Daemon status refreshes every five seconds; the server catalog can be refreshed explicitly. Account identity, signed device records, local history, and optional sync are available without showing recovery keys. Role/config changes require editing daemon configuration and restarting; the UI deliberately does not invent unsupported API controls. Disconnect dashboard only forgets API pairing, leaving the daemon running.

Public HTTPS deployment may block HTTP local API access. Serve this same dashboard locally from Vite/preview or the native companion app, and configure its exact origin. No website, public server, release download, npm package, or extension store listing has been published.

The website uses CSS and SVG diagrams with locally bundled DM Sans and Manrope fonts. It makes no font/CDN/analytics calls. No generated raster assets or image service is required.

Browser integration checks build real isolated Go daemons, pair the UI, add/select an advertised exit, verify account/history and token handling, load the Chromium extension to verify its actual proxy consent and restore behavior, and verify a personal VPS with the same account key and a separate device identity:

```sh
npx playwright install chromium
npm run build
npm run test:browser --workspace @meshway/web
```

Set `GO_BINARY` or `CHROMIUM_EXECUTABLE` if the executables are outside the default locations. Screenshots are written to `artifacts/screenshots/`. Firefox proxy runtime validation remains a release gate.

Personal servers use `meshway setup-server --personal --config server.json`, offline recovery import, and same-account device certificates. The dashboard labels actual personal advertisements as Private. It never asks for recovery secrets and never shows a fictional public directory.
