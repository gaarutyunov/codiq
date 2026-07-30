// Every internal href and asset URL goes through this so the site works
// unchanged at the domain root, under the /codiq/ project-Pages subpath, and
// under a /codiq/pr-preview/pr-<N>/ preview subpath. Astro's BASE_URL carries
// whatever `base` the build was given; strip its trailing slash so callers can
// pass root-relative paths ("/docs/model/") and get a correct result either way.
const root = import.meta.env.BASE_URL.replace(/\/$/, '')

/** @param {string} path a root-relative path, e.g. "/docs/model/" */
export function url(path) {
  return `${root}${path}`
}
