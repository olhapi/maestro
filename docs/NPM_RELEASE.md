# npm Release Runbook

This runbook covers the Docker-backed launcher release flow for `@olhapi/maestro`.

## One-command release

Use the release helper when you want the repository to drive the full publish flow from a clean `main` checkout:

```bash
./scripts/publish_npm_release.sh v0.1.0-rc.2
```

The script will:

- fetch tags and fast-forward `main` from `origin/main`
- run `pnpm verify:pre-push`, including Docker image build smoke plus tarball, registry, and curl-installer launcher install smokes
- create and push the annotated release tag
- wait for `.github/workflows/release-npm.yml`
- verify npm dist-tags when GitHub trusted publishing succeeds
- fall back to downloading the root launcher artifact and publishing it locally when the workflow built the Docker image and launcher package successfully but `publish-npm` was skipped or failed

If the tag already exists on `origin`, rerunning the helper resumes that release instead of trying to recreate the tag.

## Release workflow jobs

`Release npm Package` now runs these stages:

- `go-test`
- `publish-ghcr`
- `build-root-package`
- `registry-install-smoke`
- `publish-npm`

`publish-ghcr` builds and pushes the multi-arch runtime image to GHCR before the npm smoke tests run. The smoke jobs install only the launcher package and point it at the just-published image tag.

## Manual fallback publish

Leave the GitHub repository variable `NPM_PUBLISH_ENABLED` unset or set to `false` if you want to exercise the workflow without GitHub-side npm publishing. The workflow will still build and publish the GHCR image, pack the launcher tarball, and run the launcher install smokes.

If `publish-npm` is skipped or fails after the image and launcher smoke jobs succeed, the helper downloads the `npm-root-package` artifact and publishes:

```bash
VERSION=0.1.0-rc.1

npm whoami
npm publish --access public --tag next "dist/npm/olhapi-maestro-${VERSION}.tgz"
```

For stable tags, use `--tag latest`.

If `npm whoami` fails with `E401`, refresh the maintainer session before publishing:

```bash
npm login --scope=@olhapi --registry=https://registry.npmjs.org/
npm whoami
```

## Enable trusted publishing

Configure npm trusted publishing for `@olhapi/maestro`:

- GitHub owner: `olhapi`
- GitHub repository: `maestro`
- Workflow file: `.github/workflows/release-npm.yml`
- Workflow filename in npm settings: `release-npm.yml`

Once trusted publishing is enabled:

1. Set the GitHub repository variable `NPM_PUBLISH_ENABLED=true`.
2. Leave `NPM_TOKEN` unset; the workflow publishes through GitHub Actions OIDC.
3. Keep npm package 2FA protection enabled and remove or restrict legacy automation tokens.

## Ongoing tag flow

Future tags use the same workflow:

- prerelease tags such as `v0.1.0-rc.2` publish the launcher to the npm `next` dist-tag
- stable tags such as `v0.1.0` publish the launcher to the npm `latest` dist-tag
- the GHCR runtime image publishes for every tag and is what the launcher runs by default
