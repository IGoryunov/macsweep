.PHONY: build test fmt-check bench

build:
	go build -o macsweep ./cmd/macsweep

test:
	go vet ./... && go test ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }

bench:
	go test -run XXX -bench . ./internal/scan

app:
	./packaging/build-app.sh

install-app: app
	@dest=/Applications; [ -w /Applications ] || dest="$$HOME/Applications"; mkdir -p "$$dest"; \
	rm -rf "$$dest/Macsweep.app" && cp -R dist/Macsweep.app "$$dest/" && echo "installed $$dest/Macsweep.app"

# Puts a `macsweep` command on PATH pointing at the installed app's binary,
# so the terminal (TUI, --json, undo, docker prune) and the app share one build.
install-cli:
	@app=/Applications/Macsweep.app/Contents/MacOS/macsweep; [ -x "$$app" ] || app="$$HOME/Applications/Macsweep.app/Contents/MacOS/macsweep"; \
	for d in /opt/homebrew/bin /usr/local/bin "$$HOME/.local/bin"; do \
	  if [ -d "$$d" ] && [ -w "$$d" ] || { [ "$$d" = "$$HOME/.local/bin" ] && mkdir -p "$$d"; }; then \
	    ln -sfn "$$app" "$$d/macsweep" && echo "linked $$d/macsweep -> $$app"; \
	    case ":$$PATH:" in *":$$d:"*) ;; *) echo "note: $$d is not on your PATH";; esac; exit 0; fi; \
	done; echo "no writable bin directory found"; exit 1

install: install-app install-cli
