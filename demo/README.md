# WAPSI demo video

`wapsi-demo-voiced.mp4` is a narrated 51-second submission cut generated from the verified
gateway results. It covers the problem, four-bank flow, both gateway modes,
allocation results, lien/deadline handling, and majority approval.

The shorter silent source cut is retained as `wapsi-demo.mp4`.

For the live browser recording, restart the UI with the committed
`ui/vite.config.js` so `/api` points to Fabric gateway `4000`. Keep demo mode
available on gateway `4001` as the fallback. Run `scripts/verify.ps1` before
recording.
