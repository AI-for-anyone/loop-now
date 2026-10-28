# Contributing

Bug reports, small fixes, plugin integrations, and documentation improvements are welcome. For larger behavior changes, [open an issue](https://github.com/AI-for-anyone/loop-now/issues) first so the scope is clear.

## Working in the repository

Read [`AGENTS.md`](https://github.com/AI-for-anyone/loop-now/blob/main/AGENTS.md) and [`MAIN.md`](https://github.com/AI-for-anyone/loop-now/blob/main/MAIN.md) first. `MAIN.md` is the single maintained overview of the project's current state; keep changes focused and don't add parallel proposal or tracking documents.

The repository's checks run on every push and PR via [GitHub Actions](https://github.com/AI-for-anyone/loop-now/blob/main/.github/workflows/ci.yml). Run the ones relevant to your changes locally:

```sh
# Backend (from the repository root)
go vet ./cmd/... ./internal/... ./api/...
go test ./cmd/... ./internal/... ./api/...

# Web (from web/, with Node 24.21.0 active)
./bin/npm24 run typecheck
./bin/npm24 run api:check
./bin/npm24 run lint
./bin/npm24 test
./bin/npm24 run build

# Plugins (from plugins/)
npm ci --ignore-scripts
npm run build
npm test
```

Database integration tests require an isolated test database through `LOOP_NOW_TEST_DATABASE_URL`; tests using Redis also need `LOOP_NOW_TEST_REDIS_URL`. Native and browser tests have additional fixture requirements. **A skipped integration test is not a successful integration run.**

Keep credentials, local runtime data, and build artifacts out of contributions.

## Contributing to this website

The site lives in [`docs/`](https://github.com/AI-for-anyone/loop-now/tree/main/docs/) as a VitePress project:

```sh
cd docs
npm ci
npm run dev      # local preview on port 7100
npm run build    # static output in docs/.vitepress/dist
```

Every page has an **Edit this page on GitHub** link. Content tracks the repository's README and specs — when those change, update the matching page here in the same PR.

## License

Copyright 2026 Loop Now contributors. Loop Now is licensed under the [Apache License 2.0](https://github.com/AI-for-anyone/loop-now/blob/main/LICENSE). Bundled third-party code and fonts retain their own licenses and notices.
