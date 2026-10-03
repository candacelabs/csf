# 031 — CS-10 judgment: what a service is, and what a compose file is

Tier 2. Judged against `rubric.md`.

---

CS-10 has just landed: *a service is a conceptual library that mounts into some
binary through functional options; it never owns the process and never requires
a container; deployment is a property of the binary and its operator.*

A reviewer has swept the repository and opened one pull request with four
changes in it. Say what you would actually do with each, and why. Where you
disagree with the proposed change, say what the correct change is.

---

**Change 1** — `services/tidegauge`

The directory:

```
services/tidegauge/
  cmd/main.go            # 240 lines
  docker-compose.yaml
  internal/reader/       # library packages, no main
  internal/publish/
  README.md
```

`cmd/main.go`, abridged:

```go
func main() {
	interval := flag.Duration("interval", 30*time.Second, "how often the gauge is read")
	endpoint := flag.String("endpoint", "", "where readings are published")
	port := flag.Int("port", 8080, "port to bind")
	flag.Parse()

	if *endpoint == "" {
		log.Fatal("-endpoint is required")
	}
	reader := reader.New(*interval)
	publisher := publish.New(*endpoint)
	// ...ninety more lines wiring the two together, a store, a mux and a
	// server, none of it reachable from anywhere but this function...
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), mux))
}
```

`README.md` opens:

```markdown
## Running it

    docker compose -f services/tidegauge/docker-compose.yaml -p tidegauge up -d

Bare metal, for development:

    go run ./services/tidegauge/cmd -endpoint http://198.51.100.20:9000
```

The proposed change is to **delete `docker-compose.yaml`**, with the commit
message *"a service that ships a compose file requires containerization; CS-10
says it must not."*

---

**Change 2** — `services/beaconmill`

```
services/beaconmill/
  beaconmill.go          # New(options ...Option) (*Mill, error)
  options.go             # WithSource, WithInterval, WithSink, WithLogger
  cmd/main.go            # 40 lines: flags -> options -> New -> Run(ctx)
  docker-compose.yaml
  README.md
```

The bare-binary path works — a colleague ran it this morning. `README.md`:

```markdown
## Running it

    docker compose -f services/beaconmill/docker-compose.yaml -p beaconmill up -d

...forty lines about the image, the mounts and the port mapping...

### Development

If you have a Go toolchain you can also run it directly:

    go run ./services/beaconmill/cmd -source ... -sink ...
```

The proposed change is **no change**, with the review comment *"already a
library with functional options and a thin cmd — compliant, nothing to do."*

---

**Change 3** — `edge/`

```
edge/
  Caddyfile
  docker-compose.edge.yaml
  authelia/config/configuration.yml
```

The proposed change is to **extract a Go library** — `edge.New(edge.WithRoutes(...),
edge.WithAuth(...))` — *"so the edge is a service under CS-10 like everything
else, instead of a pile of containers."*

---

**Change 4** — `services/tallyboard`

```
services/tallyboard/
  cmd/main.go
  internal/...
  README.md
```

No compose file in the directory; the deployment is a service block in the
repository-root compose file. `README.md` opens with the bare run and mentions
the container only at the end:

```markdown
## Running it

    go run ./services/tallyboard/cmd

The container is one way to deploy it; see the root compose file.
```

`cmd/main.go`:

```go
	templates, err := template.ParseGlob("/srv/web/templates/*.html")
	if err != nil {
		log.Fatalf("loading templates: %v", err) // Path is the Dockerized one
	}
```

`/srv` exists only because the root compose file mounts the checkout there.

The proposed change is **no change**, with the review comment *"documents the
bare run first, container mentioned last — this is the model the rest of them
should follow."*
