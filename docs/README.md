# facility-layout documentation site

The documentation site for the **Facility Layout** bounded context, published
to <https://iqvo.github.io/facility-layout/>.

## Local development

```sh
npm ci
npm start          # dev server on http://localhost:3000/facility-layout/
```

## Build

```sh
npm run build      # static output in ./build; fails on broken internal links
npm run serve      # serve the built output locally
npm run typecheck  # tsc
```

## Regenerating the API reference

The pages under `docs/api-reference/rest/` are **generated** from this
repository's real specification at `../apis/openapi.yaml`. `npm run build`
regenerates them automatically; to do it by hand:

```sh
npm run gen-api-docs     # regenerate from ../apis/openapi.yaml
npm run clean-api-docs   # remove the generated output
```

Do not hand-edit anything under `docs/api-reference/rest/` — edit
`apis/openapi.yaml` at the repository root instead.

## Structure

```
docs/                     the markdown content
  overview/               introduction, getting started
  business-context/       domain vision, ubiquitous language, the location code
  ddd/                    subdomain classification, aggregates, invariants,
                          domain events, hexagonal architecture
  api-reference/          conventions, endpoint catalogue, drawing the warehouse,
                          bulk import, + generated rest/ from openapi.yaml
  ecosystem/              context map, consuming this service
  mcp/                    the MCP governance charter
  analytics/              the catalog-growth report contract
  adr/                    architecture decision records (Nygard format)
src/pages/index.tsx       landing page
sidebars.ts               the eight top-level categories, in order
docusaurus.config.ts      site config, openapi-docs + mermaid plugins
```

## Deployment

`.github/workflows/docs.yml` builds this site and deploys it to GitHub Pages
on every push to `main` that touches `docs/**`.
