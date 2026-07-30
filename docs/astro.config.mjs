// @ts-check
import { defineConfig } from 'astro/config'

// GitHub Pages serves this site from a subpath, and the PR previews from a
// deeper one, so the base has to be injected at build time rather than hard
// coded. `.github/workflows/docs.yml` sets BASE_PATH:
//
//   push to main   -> /codiq/                        (project Pages site)
//   pull_request   -> /codiq/pr-preview/pr-<N>/      (rossjrw/pr-preview-action)
//
// If a custom domain (e.g. codiq.garutyunov.com) is configured for Pages later,
// the site moves to the domain root: change the workflow to pass "/" and
// "/pr-preview/pr-<N>/" respectively. Nothing else needs to change — every
// internal link and asset URL in the site is prefixed from import.meta.env.BASE_URL.
const base = process.env.BASE_PATH || '/'

export default defineConfig({
  site: 'https://gaarutyunov.github.io',
  base,
  trailingSlash: 'always',
  build: {
    // Emit `about/index.html` rather than `about.html` so that directory-style
    // URLs resolve under any of the bases above without a server rewrite.
    format: 'directory',
  },
})
