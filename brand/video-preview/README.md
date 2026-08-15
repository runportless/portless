# Portless explainer preview

This is a short code-rendered explainer built from real captures of the local Portless UI.

```sh
npm install
npm run build
```

The finished video is written to `out/portless-explainer-preview.mp4`. The build renders the Remotion composition, generates narration with Microsoft's Andrew multilingual neural voice, then masters both into the final MP4 with FFmpeg. The first build creates an isolated Python environment and installs the pinned voice dependency automatically.

The voice step sends only the contents of `narration.txt` to Microsoft's speech service. It does not send source code, screenshots, traffic, or other Portless data.

Run `npm run preview` to open Remotion Studio for visual editing. The narration copy lives in `narration.txt`; the composition lives in `src/PortlessExplainer.tsx`.
