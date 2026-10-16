# Development

## Build and test

```sh
go build -o dbak ./cmd/dbak
go test ./...
```

The integration tests need Docker and the client tools:

```sh
docker compose -f docker-compose.test.yml -p dbaktest up -d
DBAK_IT_PG_BIN=/path/to/postgresql-18/bin \
DBAK_IT_MYSQL_BIN=/path/to/mysql/bin \
DBAK_IT_MARIADB_BIN=/path/to/mariadb/bin \
go test -tags integration ./internal/engine/ ./internal/backup/
```

The end-to-end tests run dbak in Linux containers:

```sh
./test/e2e/init.sh
./test/e2e/schedule.sh
./test/e2e/install.sh
./test/e2e/restore.sh
```

`restore.sh` makes an encrypted backup, removes the VM container, and restores
the backup without dbak.

To stop the test containers, run
`docker compose -f docker-compose.test.yml -p dbaktest down -v`.

## Release

1. Make sure that the CI workflow passes on `main`.
2. Make a tag, for example `git tag v1.0.0`.
3. Push the tag with `git push origin v1.0.0`.

The release workflow runs GoReleaser. GoReleaser puts the binaries for
`linux/amd64` and `linux/arm64`, `checksums.txt`, and `install.sh` on the GitHub
Releases page.

To build a release locally without a publish, run
`goreleaser release --snapshot --clean`.
