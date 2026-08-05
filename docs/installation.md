# Installation

CantiNode is currently source-only — no tagged releases or prebuilt
binaries yet (see [Roadmap](../ROADMAP.md)).

## From source

Requires Go 1.25+ and Node.js 22+ (only needed to build the frontend;
the resulting binary has no Node.js runtime dependency).

```sh
git clone https://github.com/obstruct-exit-emit/CantiNode.git
cd CantiNode
make build
./cantinode
```

`make build` builds the frontend into `web/dist` and then the Go binary,
which embeds it via `go:embed` — see
[Development](development.md#why-make-build-not-go-build) for why a plain
`go build` alone isn't enough after a fresh clone or an update.

CantiNode listens on port 7847 by default and writes its SQLite database
and `config.yaml` (if one doesn't already exist) under `./data`. See
[Configuration](configuration.md) for every setting.

## Updating an existing from-source install

```sh
git pull
make build
```

Same reasoning as the initial build: `make build`, not a bare `go build`,
so the frontend actually gets rebuilt from the pulled source rather than
silently reusing whatever was already sitting in `web/dist`.

## Running as a service

No systemd unit is packaged yet (see [Roadmap](../ROADMAP.md)). In the
meantime, run `./cantinode` under whatever process supervisor you already
use (systemd, a Docker container, `screen`/`tmux`, ...), from the
directory containing (or set to contain) its `config.yaml` and `data/`.
