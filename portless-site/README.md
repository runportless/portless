# Portless Website

This is the static marketing site for [www.portless.run](https://www.portless.run).
It is intentionally separate from `portless-web`, the React control plane that
is embedded in the Portless executable.

## Local development

```bash
npm ci
npm run dev
```

From the repository root, use `make site-dev` to run the local development
server, `make site` for a production build, or `make test-site` for type
checking, tests, and a production build.

The optional `PUBLIC_EARLY_ACCESS_URL` environment variable changes the primary
call to action from the on-page demo to an external early-access form.
`PUBLIC_GITHUB_URL` can override the canonical repository link. Copy
`.env.example` to `.env` for local overrides; local environment files are
ignored by Git.

## Product imagery

All product screenshots in `src/assets/product` and the video poster are direct
captures from a running Portless application. Do not use explainer-video frames
as substitutes for product UI. The explainer itself remains available as the
captioned neural-voice MP4 in `public/demo`.

## GitHub Pages

The `Portless website` workflow tests and builds pull requests, then deploys
`portless-site/dist` after pushes to `main`. In the repository's Pages settings,
select **GitHub Actions** as the publishing source and set the custom domain to
`www.portless.run`. The built artifact includes the matching `CNAME` file.

The workflow reads optional repository variables named
`PORTLESS_EARLY_ACCESS_URL` and `PORTLESS_GITHUB_URL`.
