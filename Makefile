# CantiNode build — see docs/development.md for the full picture.
#
# The frontend must be built into web/dist before the Go binary is built:
# go:embed bakes in whatever's already on disk in web/dist at build time,
# not what's in git — web/dist's actual built files are gitignored, only a
# placeholder .gitkeep is committed (see .gitignore). A plain `git pull &&
# go build`, with no frontend step, silently succeeds and produces a real,
# runnable binary — just one still serving whatever UI happened to already
# be sitting in web/dist from an earlier build. `make build` closes that
# gap by always doing both steps, in the right order, as one command.

.PHONY: build frontend build-backend-only test clean

build: frontend
	go build ./cmd/cantinode

frontend:
	cd web && npm ci && npm run build

# build-backend-only skips the frontend step, embedding whatever is
# already in web/dist as-is — for building on a machine with Node.js and
# copying just the resulting binary to a box that deliberately doesn't
# have it. A separate, explicitly-named target rather than a flag on
# `build`: skipping the frontend has to be typed on purpose.
build-backend-only:
	go build ./cmd/cantinode

test:
	go vet ./...
	go test ./...

clean:
	rm -f cantinode cantinode.exe
