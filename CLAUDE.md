# CLAUDE.md

Go CLI (`github.com/oskarrn93/dispatcharr-rematch`) that re-matches Dispatcharr channels to the best-quality stream. Scheduled by the `homelab` repo, not here.

- `go vet ./... && go test ./...` before committing; conventional commits drive the semver release.
- Never print the credentials file; only `dispatcharr-rematch.env.example` is committed.
- Telemetry metric and log names are consumed by alerts and a dashboard in `homelab`; renaming them breaks those.
