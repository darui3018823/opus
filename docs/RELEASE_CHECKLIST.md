# Release Checklist

This checklist is the repeatable release path for the module. The current
released baseline is `v1.4.0`; do not infer the next version from that fact.
Choose the version from the accumulated changes, and obtain explicit user
approval before changing `VERSION`, creating or pushing a tag, or publishing a
GitHub Release.

`VERSION` is the only hand-edited version source. `version_gen.go` is generated
from it and must never be edited directly. GitHub Release notes are the
canonical per-version change record; do not rewrite notes for an existing
release to describe later `main` behavior.

## 1. Prepare the candidate

- Start from a clean, reviewed release branch based on the intended `main`
  commit. Fetch the remote and tags, then confirm the baseline:

  ```powershell
  git fetch --tags origin
  git describe --tags --abbrev=0
  Get-Content VERSION
  git status --short
  ```

- Inventory everything since the baseline, including exported API additions,
  observable behavior changes, fixes, documentation, CI, and security work:

  ```powershell
  git log --first-parent --oneline v1.4.0..HEAD
  git diff --stat v1.4.0..HEAD
  go doc -all .
  go doc -all ./oggopus
  ```

- Review [`V2_API_CANDIDATES.md`](V2_API_CANDIDATES.md). Do not fold a listed
  breaking cleanup into a v1 release.
- Select the version using SemVer:
  - patch for backward-compatible fixes and documentation only;
  - minor for backward-compatible public functionality or API additions;
  - major for an intentional compatibility break.
- Do not select patch merely because each individual change looks small. The
  version reflects the complete diff from the last release.

The `v1.4.0` release included backward-compatible public additions such as
explicit-duration and additional-PCM PLC/FEC variants for single-stream and
multistream decoders. Re-run the inventory for every later release instead of
inferring a patch or minor version from that historical classification.

## 2. Update version and documentation

- After approval of the chosen version, edit `VERSION` only. It must contain a
  canonical `major.minor.patch` value without a `v` prefix, prerelease suffix,
  build metadata, or leading zeroes.
- Regenerate and inspect the exact version diff:

  ```powershell
  go generate ./...
  git diff -- VERSION version_gen.go
  go test -count=1 -run '^TestVersionMetadata$' .
  ```

- Update `docs/CURRENT_IMPLEMENTATION.md`, both READMEs, CTL/support tables,
  `SECURITY.md`, and other affected documentation from the candidate code.
  Keep English and Japanese README claims in semantic parity.
- Draft release notes with these sections where applicable:
  - summary and compare link from the preceding tag;
  - new public API and behavior;
  - bug and security fixes;
  - compatibility or migration notes;
  - known limitations;
  - verification performed.
- Call out optional CGO/libopus checks as reference validation, not a runtime
  dependency. Do not claim bit-exact encoder parity.

## 3. Qualify the exact commit

Run the locally reproducible gates from PowerShell:

```powershell
go fmt ./...
go generate ./...
git diff --exit-code
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go test -count=1 -tags opusref ./...
go run github.com/rhysd/actionlint/cmd/actionlint@latest -color
```

The `git diff --exit-code` check is run after the intended version and
documentation changes have been committed; it proves that generation adds no
new drift. Also confirm that:

- `TestOfficialVectors` actually ran with all 12 RFC 8251 vectors rather than
  skipping because local vector data was absent;
- the four public examples ran;
- repository-relative README and documentation links resolve;
- every required hosted CI job is green on the exact candidate commit,
  including the six native OS/architecture cells and Ubuntu `opusref` job;
- the release notes and supported-version table describe that commit.

## 4. Approve, tag, and publish

Stop here until the user explicitly approves the version bump and release.
After approval:

1. Confirm the approved candidate is the reviewed `main` commit, the worktree
   is clean, and all required CI checks are green.
2. Confirm the tag and GitHub Release do not already exist. The tag must be
   exactly `v` followed by the contents of `VERSION`.
3. Create an annotated tag so future releases use one consistent policy:

   ```powershell
   $releaseVersion = (Get-Content -Raw VERSION).Trim()
   $releaseTag = "v$releaseVersion"
   git rev-parse --verify HEAD
   git show-ref --verify --quiet "refs/tags/$releaseTag"
   if ($LASTEXITCODE -eq 0) { throw "local tag $releaseTag already exists" }
   git ls-remote --exit-code --tags origin "refs/tags/$releaseTag" | Out-Null
   if ($LASTEXITCODE -eq 0) { throw "remote tag $releaseTag already exists" }
   git tag -a $releaseTag -m "Release $releaseTag"
   git show --no-patch --decorate $releaseTag
   ```

   The `git rev-parse` tag lookup must fail before tag creation. Decide whether
   signed tags are required before creating the tag; do not claim provenance
   that was not produced.
4. Push the already-reviewed commit, then push only the approved tag. Verify
   that the remote tag resolves to the exact candidate commit.
5. Publish the GitHub Release from that existing tag using the reviewed notes.
   This source-only Go module does not require binary assets by default.

## 5. Verify after publication

- Re-check the tag's `VERSION`, generated constants, commit ID, and Release
  notes.
- After the module proxy observes the tag, test a fresh consumer outside this
  repository:

  ```powershell
  $releaseVersion = (Get-Content -Raw VERSION).Trim()
  $releaseTag = "v$releaseVersion"
  $smokeName = "opus-release-smoke-" + [guid]::NewGuid().ToString("N")
  $smokeDir = Join-Path ([System.IO.Path]::GetTempPath()) $smokeName
  New-Item -ItemType Directory -Path $smokeDir | Out-Null
  Push-Location $smokeDir
  go mod init example.com/opus-release-smoke
  go get "github.com/darui3018823/opus@$releaseTag"
  go list -m all
  Pop-Location
  ```

- Update project status records only after publication is verified.

## 6. Recovery rules

- Before a tag is pushed, fix the candidate with normal commits. An incorrect
  local tag may be deleted and recreated after its target is re-verified.
- A draft GitHub Release may be deleted before publication, but deleting it
  does not delete or invalidate a pushed tag.
- Once a tag or module version is public, never silently move or reuse it. Go
  module proxies and consumers may retain the original content permanently.
- For a non-security defect in a public release, revert or fix on `main` and
  publish a new patch release.
- For a harmful or security-sensitive release, coordinate a security advisory,
  ship a corrected release, and consider a `retract` directive in `go.mod` in a
  subsequent version. Retraction is a warning to consumers, not deletion.
- If a tag was pushed from the wrong commit, do not retag the same version after
  it may have been fetched. Publish a corrected version and explain the mistake
  in its release notes.
