# Installation

CantiNode is a single self-contained binary; the web UI is embedded. Data
(config, database, logs, backups) lives in one directory: `~/.config/cantinode`
by default, or wherever `--data <dir>` points.

> **Docker and Windows builds are on hold for now** — planned to return later
> (see the [roadmap](../ROADMAP.md)). Linux (bare metal or from source) is the
> supported path today.

## Linux (bare metal)

Download the release tarball (or build from source), then:

```sh
sudo useradd --system --home /var/lib/cantinode --create-home cantinode
sudo cp cantinode /usr/local/bin/
sudo cp packaging/systemd/cantinode.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cantinode
```

The unit ships with filesystem hardening — add your media folders to
`ReadWritePaths=` so the scanner and organizer can touch them.

## From source

Requires Go 1.25+ and Node 22+:

```sh
cd web && npm install && npm run build && cd ..
go build ./cmd/cantinode
./cantinode
```

Open `http://localhost:7845` when it's running.
