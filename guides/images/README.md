# Screenshot capture notes

These JPEGs were captured on September 7, 2026 from a running Portless application
and VS Code on macOS. They use the real Store example, with Node.js checkout and
orders services, Spring Boot inventory, PostgreSQL, and Valkey.

The capture project was named `store-guides` and used a separate copy of
`examples/store`, with fresh managed resources. `local` was the primary
environment; `experiment` was created through the dashboard. Order IDs,
timestamps, process IDs, inspector ports, and generated worktree paths are
specific to that run.

To refresh a screenshot:

1. Build the current application with `make` and run that executable. When
   replacing an already running development daemon, use its normal restart
   workflow so the dashboard serves the new embedded assets.
2. Follow the corresponding guide against a dedicated Store project. Disable
   mocks and faults before starting a different scenario.
3. Capture the actual UI state after the action completes. Keep navigation,
   environment identity, and the relevant result visible. Browser captures here
   use a 1280 × 720 viewport; VS Code captures show its application window.
4. Replace the JPEG in that guide's directory and check its Markdown caption
   and instructions against the new image.
5. Disconnect the debugger, disable experimental mocks and faults, stop active
   recordings, and stop the environments created for the capture session.

Use application screenshots directly. Do not substitute mockups or video frames,
or edit a displayed value to stand in for an action that was not performed.
