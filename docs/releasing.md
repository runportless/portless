# Releasing Portless

Portless releases are created from signed SemVer tags in the canonical
[`runportless/portless`](https://github.com/runportless/portless) repository.
They are published under the Apache-2.0 license. The tag workflow builds macOS
and Linux archives for AMD64 and ARM64, creates checksums, SPDX JSON SBOMs, and
build-provenance attestations, and proposes an update to
[`runportless/homebrew-tap`](https://github.com/runportless/homebrew-tap).

There are two manual publication gates:

| Gate | What is ready | Manual action |
| --- | --- | --- |
| GitHub release | Tested artifacts are staged in a draft release. | Approve the protected `release` environment. |
| Homebrew bottles | The tap pull request has passed its formula and bottle checks. | Apply the `pr-pull` label to the tap pull request. |

Everything before each gate is staged or tested but not published through that
gate. Pushing a release tag starts the workflow; it does not bypass either
manual review.

The Homebrew formula must always be addressed as
`runportless/tap/portless`. Homebrew Core contains an unrelated formula named
`portless`, so do not shorten the install or uninstall name.

## Choose the release type

| Release type | Tag format | Example | Homebrew decision |
| --- | --- | --- | --- |
| Stable | `vMAJOR.MINOR.PATCH` | `v1.2.3` | Apply `pr-pull` after the tap checks pass. |
| Prerelease | `vMAJOR.MINOR.PATCH-PRERELEASE` | `v1.2.3-alpha.1` | Apply `pr-pull` only when the prerelease should become the tap's default formula. |

GitHub automatically marks a SemVer prerelease accordingly. To test the full
proposal path without publishing that prerelease through Homebrew, allow the
tap pull request checks to finish and do not apply `pr-pull`.

Before declaring the first stable release, review the
[implementation status](implementation-status.md) and explicitly accept or
resolve its deferred public-release gates.

## Per-release checklist

### 1. Choose an unused version

- [ ] Select the next stable or prerelease SemVer version.
- [ ] Confirm that no tag or GitHub release already uses that version.
- [ ] Prepare a concise release summary for the annotated tag message or
      associated release notes.
- [ ] Resolve any older open Portless formula pull request. Publish it, or
      close it and delete its release branch if it was only a plumbing test.

Published tags and artifacts are immutable. A correction always receives a
new version.

### 2. Synchronize and inspect `main`

Run from the repository root:

```bash
git switch main
git pull --ff-only
git status --short
```

- [ ] Confirm `git status --short` prints nothing.
- [ ] Confirm every intended change is merged and pushed to `main`.
- [ ] Confirm CI is green for the exact commit that will be tagged.
- [ ] Confirm the tracked `portless-web/dist` assets are current.

Do not tag an unmerged commit. The release workflow rejects a tag whose commit
is not an ancestor of `origin/main`.

### 3. Run the local preflight

```bash
make test
make test-e2e-cli
make release-check
make release-snapshot
git diff --check
git status --short
```

- [ ] Confirm every command succeeds.
- [ ] Confirm the final `git status --short` still prints nothing.
- [ ] Confirm the snapshot contains the expected macOS and Linux archives.

`make release-snapshot` deliberately skips SBOM generation so local preflight
does not require Syft. The tagged workflow installs Syft and requires both
archive and source SBOMs.

Do not run the machine-destructive relay E2E suites as release preflight. A
fresh-machine `portless setup` smoke test is a separate machine integration
test and requires explicit authorization.

### 4. Create and push the signed tag

For a stable release, replace `1.2.3` with the chosen version:

```bash
git tag -s v1.2.3 -m "Portless 1.2.3"
git tag -v v1.2.3
git push origin v1.2.3
```

For a prerelease:

```bash
git tag -s v1.2.3-alpha.1 -m "Portless 1.2.3 alpha 1"
git tag -v v1.2.3-alpha.1
git push origin v1.2.3-alpha.1
```

- [ ] Verify the tag signature before pushing.
- [ ] Push only the release tag; do not create the GitHub release manually.
- [ ] Open the tag-triggered **Release** workflow in GitHub Actions.

The workflow now validates the tag and `main` ancestry, runs the release tests,
builds the artifacts, verifies every checksum, smoke-tests the Linux AMD64
binary, and creates the SBOMs and provenance attestations. GoReleaser keeps the
GitHub release in draft form during this work.

### 5. Gate 1: approve the GitHub release

- [ ] Wait for **Build and stage GitHub release** to succeed.
- [ ] Inspect the staged version and workflow results before approving.
- [ ] In the pending deployment, approve the protected `release` environment.
- [ ] Wait for **Approve and publish GitHub release** to succeed.
- [ ] Confirm the GitHub release is public and points to the intended tag.
- [ ] For a prerelease, confirm GitHub marks it as a prerelease.
- [ ] Confirm the release includes archives, `checksums.txt`, and SPDX JSON
      SBOMs.

Approval makes the staged GitHub release public. The workflow then renders the
Homebrew formula from the released source archive's checksum and opens or
updates a tap pull request.

### 6. Gate 2: review the Homebrew pull request

- [ ] Confirm the formula URL points to `runportless/portless` and the expected
      release tag.
- [ ] Confirm the formula SHA-256 matches the source archive entry in the
      GitHub release's `checksums.txt`.
- [ ] Wait for every generated formula and bottle check to pass.
- [ ] Decide whether this version should be published through Homebrew.

If the answer is **yes**:

- [ ] Apply the `pr-pull` label.
- [ ] Wait for the generated `brew pr-pull` workflow to publish the bottles,
      commit the bottle metadata, update the tap, and remove the release
      branch.
- [ ] Confirm the tap pull request is merged and the publisher workflow is
      green.

If the answer is **no**, as with a prerelease used only to test the plumbing:

- [ ] Leave `pr-pull` unapplied.
- [ ] After the checks prove the proposal path, close the pull request and
      delete its release branch so it cannot be published accidentally later.

### 7. Verify the published release

After Homebrew publication, verify the formula on Apple Silicon and Intel
macOS when both are available:

```bash
brew update
brew install runportless/tap/portless
portless --version
portless doctor relay
```

On a machine that already has the formula installed, replace `brew install`
with `brew upgrade runportless/tap/portless`.

- [ ] Confirm `portless --version` reports the released version.
- [ ] Confirm `portless doctor relay` does not report a binary mismatch.
- [ ] Record any intentionally skipped architecture or machine-level smoke
      test in the release notes or release tracking issue.

To verify downloaded release artifacts on Linux:

```bash
sha256sum -c checksums.txt --ignore-missing
gh attestation verify portless_1.2.3_linux_amd64.tar.gz \
  --repo runportless/portless
```

On macOS, select the downloaded archive before checking it:

```bash
grep 'portless_1.2.3_darwin_arm64.tar.gz' checksums.txt | shasum -a 256 -c -
gh attestation verify portless_1.2.3_darwin_arm64.tar.gz \
  --repo runportless/portless
```

## What the release contains

Each stable or prerelease tag produces:

- macOS and Linux archives for AMD64 and ARM64;
- a source archive used to build the Homebrew formula;
- SHA-256 checksums;
- SPDX JSON archive and source SBOMs;
- GitHub build-provenance attestations; and
- a proposed `Formula/portless.rb` update in the tap.

The Homebrew formula compiles the tracked embedded web assets with the current
Go toolchain and links:

- `Version` to the formula version; and
- `Distribution` to `homebrew`, which prevents `portless uninstall` from
  deleting a Homebrew-owned launcher.

## One-time repository and tap setup

Complete this section once. It is not part of the recurring release checklist.

### Create the tap

On a maintainer Mac with Homebrew and GitHub CLI authentication:

```bash
brew tap-new --github-packages runportless/tap
gh repo create runportless/homebrew-tap \
  --public \
  --source "$(brew --repository runportless/tap)" \
  --push
```

- [ ] Keep the workflows generated by `brew tap-new`; they test formula pull
      requests and run `brew pr-pull` after the label is applied.
- [ ] If tap `main` is protected, allow the generated publisher workflow to
      push its bottle commit through the ruleset.

### Configure the release GitHub App

- [ ] Create a GitHub App dedicated to release automation.
- [ ] Install it on the `runportless` organization with access limited to
      `homebrew-tap`.
- [ ] Grant **Contents: read and write**.
- [ ] Grant **Pull requests: read and write**.
- [ ] In `runportless/portless`, set the Actions variable
      `HOMEBREW_TAP_APP_CLIENT_ID` to the App client ID.
- [ ] In `runportless/portless`, set the Actions secret
      `HOMEBREW_TAP_APP_PRIVATE_KEY` to one App private key.

The workflow exchanges those credentials for a short-lived installation token
scoped to `runportless/homebrew-tap`. The source repository's normal
`GITHUB_TOKEN` publishes only the Portless GitHub release.

### Configure GitHub environments

- [ ] Create a protected environment named `release`.
- [ ] Add the publishing maintainer as a required reviewer.
- [ ] Allow self-review when that maintainer also pushes the release tag.
- [ ] Create the `homebrew` environment without a required reviewer. That job
      can only propose a formula pull request; `pr-pull` remains the bottle
      publication gate.

## Failure checklist

### The build fails before GitHub publication

- [ ] Do not approve the `release` environment.
- [ ] Inspect and correct the failing stage.
- [ ] Rerun transient failures; GoReleaser can replace the existing draft for
      the same tag.
- [ ] If source changes are required, use a new version rather than publishing
      altered artifacts under the old tag.

### The GitHub release is already public

- [ ] Do not replace its tag or artifacts.
- [ ] Correct the problem in source and publish a new patch or prerelease
      version.

### Only the Homebrew proposal or bottle job fails

- [ ] Rerun the failed job; formula branch and pull-request creation are
      idempotent.
- [ ] Correct a formula problem before applying `pr-pull`.
- [ ] If a bad formula was already published, stop or revert its tap update and
      publish a corrected Portless patch release.

A Homebrew upgrade may leave the privileged relay helper from the previous
binary in place. `portless setup` detects and refreshes that copy, while
`portless doctor relay` reports the mismatch without disrupting active
environments.
