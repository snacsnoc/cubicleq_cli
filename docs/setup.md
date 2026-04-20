# setup - build and initialize `cubicleq`

## SYNOPSIS

```bash
go build -o ./bin/cubicleq ./cmd/cubicleq
./bin/cubicleq init
```

```bash
./bin/cubicleq --root /path/to/repo init --bootstrap-git
```

## DESCRIPTION

Build with raw Go commands.

Use `./scripts/build.sh` only when repo-local Go caches are required.

```bash
./scripts/build.sh
```

## EXAMPLES

```bash
mkdir -p .cache/go-build .cache/go-mod .cache/go-tmp
GOCACHE=$(pwd)/.cache/go-build \
GOMODCACHE=$(pwd)/.cache/go-mod \
GOTMPDIR=$(pwd)/.cache/go-tmp \
GOSUMDB=off \
go build -o ./bin/cubicleq ./cmd/cubicleq
```

```bash
./scripts/smoke_e2e.sh
```

## OPTIONS

`scripts/smoke_e2e.sh`:
- `--keep` keep the scratch repo after run
- `--skip-orchestrate` skip `cubicleq orchestrate`
- `--repo-dir PATH` use a specific empty directory
