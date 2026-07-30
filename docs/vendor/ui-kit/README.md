# Vendored: @gaarutyunov/ui-kit

This directory is a vendored copy of the `src/` of
[gaarutyunov/ui-kit](https://github.com/gaarutyunov/ui-kit) — a
zero-dependency, buildless Web Components UI kit (MIT).

It is vendored (rather than installed from npm) because the package is
published to GitHub Packages, which requires authentication even for public
packages; vendoring keeps the docs build fully offline and reproducible in CI.
This mirrors what gopgql's docs site does.

- Upstream version: 0.1.0
- Upstream commit: `a7b5a7107230fa5d6aead3dcdc4e93ab030526bd`

The site imports the components from `src/index.js` and the theme from
`src/tokens/tokens.css` (see `docs/src/layouts/Base.astro`). The `.d.ts`
declarations are not copied — the site is plain JS/Astro and does not
type-check against the kit. See `LICENSE` for the upstream MIT license.

## Refreshing

    cp -R <ui-kit>/src docs/vendor/ui-kit/src
    find docs/vendor/ui-kit -name '*.d.ts' -delete

then update the version and commit recorded above.
