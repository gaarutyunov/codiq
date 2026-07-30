// The docs table of contents, in reading order. Both the header nav and the
// docs sidebar render from this, so a new page is added in exactly one place.
export const docsNav = [
  {
    href: '/docs/',
    title: 'What M1 ships',
    blurb: 'The data model and the MCP surface, over seeded data.',
  },
  {
    href: '/docs/model/',
    title: 'The core model',
    blurb: 'file, occurrence, scope — and the edges between them.',
  },
  {
    href: '/docs/identity/',
    title: 'Symbol identity',
    blurb: 'SCIP-style descriptors: one string names every symbol.',
  },
  {
    href: '/docs/mcp/',
    title: 'Query & MCP surface',
    blurb: 'GraphQL over MCP, compiled to SQL/PGQ by gopgql.',
  },
  {
    href: '/docs/pipeline/',
    title: 'The ingestion pipeline',
    blurb: 'extract → transform → load → link. Lands from M2 on.',
  },
  {
    href: '/docs/run-locally/',
    title: 'Run it locally',
    blurb: 'Docker Compose: Postgres 19 + gopgql, seeded.',
  },
]
