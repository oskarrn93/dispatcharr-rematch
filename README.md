# dispatcharr-rematch

Re-matches Dispatcharr channels to the best-quality stream from its M3U accounts. Run it with `-refresh -apply` on a schedule (every 6h); without `-apply` it only reports what it would change.

Config comes from the environment or a `KEY=VALUE` file passed with `-env-file` (default `./dispatcharr-rematch.env`); see `dispatcharr-rematch.env.example`. Keep that file mode `600`.

```sh
go test ./...
go run ./cmd/dispatcharr-rematch -refresh
docker run --rm --network host ghcr.io/oskarrn93/dispatcharr-rematch:<tag> -refresh -apply
```

Releases (`release.yml`, semver from conventional commits) publish `linux/amd64` and `linux/arm64` tarballs with `checksums.txt`, and a multi-arch image to GHCR. It is scheduled from [homelab](https://github.com/oskarrn93/homelab).

With `TELEMETRY_ENABLED=true` it exports OpenTelemetry metrics and logs to `OTEL_EXPORTER_OTLP_ENDPOINT`; the `DispatcharrRematch*` alerts and the Grafana dashboard live in homelab.
