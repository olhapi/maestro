# npm Release Runbook

This runbook covers the first public npm publish for Maestro and the steady-state tag flow after trusted publishing is enabled.

## One-command release

Use the release helper when you want the repository to drive the full publish flow from a clean `main` checkout:

```bash
./scripts/publish_npm_release.sh v0.1.0-rc.2
```

The script will:

- fetch tags and fast-forward `main` from `origin/main`
- run `pnpm verify:pre-push`
- create and push the annotated release tag
- wait for `.github/workflows/release-npm.yml`
- verify npm dist-tags when GitHub trusted publishing succeeds
- fall back to downloading the workflow artifacts and running local `npm publish` in leaf-first order when the workflow build/smoke jobs succeed but `publish-npm` is skipped or fails

If the tag already exists on `origin`, rerunning the helper will resume that release instead of trying to recreate the tag. This is intended for cases where the GitHub workflow finished but npm publish still needs the local artifact fallback.

If the fallback path is used, npm may pause for browser-based account confirmation before it can publish the tarballs.

## First public prerelease bootstrap

Leave the GitHub repository variable `NPM_PUBLISH_ENABLED` unset or set to `false`. The release workflow will still build all five native tarballs, build the root package, and run both smoke-test stages, but the `publish-npm` job will stay skipped until trusted publishing is ready.

Cut and push the first public tag:

```bash
git tag v0.1.0-rc.1
git push origin v0.1.0-rc.1
```

Wait for the `Release npm Package` workflow to finish these jobs successfully:

- `go-test`
- `build-leaf-packages`
- `build-root-package`
- `registry-install-smoke`

Download the six npm artifacts from that workflow run:

- `npm-leaf-darwin-arm64`
- `npm-leaf-darwin-x64`
- `npm-leaf-linux-x64-gnu`
- `npm-leaf-linux-arm64-gnu`
- `npm-leaf-win32-x64`
- `npm-root-package`

On a maintainer machine that is already logged in to npm for the `@olhapi` scope with 2FA enabled, publish the five leaf tarballs first, then publish the root tarball last so its optional dependencies already exist:

```bash
VERSION=0.1.0-rc.1

npm whoami

for tarball in \
  "dist/npm/olhapi-maestro-darwin-arm64-${VERSION}.tgz" \
  "dist/npm/olhapi-maestro-darwin-x64-${VERSION}.tgz" \
  "dist/npm/olhapi-maestro-linux-x64-gnu-${VERSION}.tgz" \
  "dist/npm/olhapi-maestro-linux-arm64-gnu-${VERSION}.tgz" \
  "dist/npm/olhapi-maestro-win32-x64-${VERSION}.tgz"; do
  npm publish --access public --tag next "$tarball"
done

npm publish --access public --tag next "dist/npm/olhapi-maestro-${VERSION}.tgz"
```

If `npm whoami` fails with `E401`, refresh the maintainer session before publishing:

```bash
npm login --scope=@olhapi --registry=https://registry.npmjs.org/
npm whoami
```

If `npm whoami` succeeds but `npm publish` still returns `404 Not Found` for an `@olhapi/*` package, the authenticated account does not have publish rights for the `@olhapi` scope.

Verify the bootstrap publish before enabling CI publishing:

```bash
npm view @olhapi/maestro dist-tags --json
npm view @olhapi/maestro-darwin-arm64 version
npm view @olhapi/maestro-darwin-x64 version
npm view @olhapi/maestro-linux-x64-gnu version
npm view @olhapi/maestro-linux-arm64-gnu version
npm view @olhapi/maestro-win32-x64 version
npm install -g @olhapi/maestro@next
maestro version
```

The root package should report `next: 0.1.0-rc.1`, every leaf package should exist at `0.1.0-rc.1`, and the installed CLI should report `maestro 0.1.0-rc.1`.

## Enable trusted publishing

After the bootstrap publish succeeds, configure npm trusted publishers for all six packages:

- GitHub owner: `olhapi`
- GitHub repository: `maestro`
- Workflow file: `.github/workflows/release-npm.yml`
- Workflow filename in npm settings: `release-npm.yml`

Repeat that configuration for:

- `@olhapi/maestro`
- `@olhapi/maestro-darwin-arm64`
- `@olhapi/maestro-darwin-x64`
- `@olhapi/maestro-linux-x64-gnu`
- `@olhapi/maestro-linux-arm64-gnu`
- `@olhapi/maestro-win32-x64`

Once all six packages trust the release workflow:

1. Set the GitHub repository variable `NPM_PUBLISH_ENABLED=true`.
2. Leave `NPM_TOKEN` unset; the workflow now publishes through GitHub Actions OIDC.
3. Keep npm package 2FA protection enabled and remove or restrict legacy automation tokens.

## Ongoing tag flow

Future tags use the same workflow and artifact order:

- prerelease tags such as `v0.1.0-rc.2` publish to the npm `next` dist-tag
- stable tags such as `v0.1.0` publish to the npm `latest` dist-tag
- CI publishing uses `npm publish --provenance` with trusted publishing, so no long-lived npm token is required in GitHub Actions
