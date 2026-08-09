# LibriNode Web UI

The React 19 + Vite SPA. `go build` embeds `web/dist` into the binary
(via `web.go`), so LibriNode ships as a single executable serving the API
and UI on one port.

```sh
npm install
npm run dev     # Vite dev server, proxies /api to :7845
npm run build   # production build into web/dist (then rebuild the binary)
```

Without a `dist` build the binary still compiles (`dist/.gitkeep` keeps the
embed valid) and serves a plain status page instead of the UI.
