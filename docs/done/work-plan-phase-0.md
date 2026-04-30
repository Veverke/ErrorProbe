# Phase 0 — Repository & Project Skeleton

**Goal:** A compilable Go project with the right structure, ready to receive real implementation.  
**No external services. No Docker interaction. Just a clean foundation.**

**UT coverage requirement: ≥ 90% on all new packages.**

---

## Atomic Tasks

Tasks are grouped by dependency tier. All tasks within a tier can be implemented independently and in parallel.

---

### Tier 1 — Environment (no code dependencies)

#### T0.1 — Initialise Go module
- Run `go mod init github.com/errorprobe/errorprobe`
- Set minimum Go version in `go.mod` (1.22+)
- Add `.gitignore` (Go standard: binaries, `vendor/`, `*.exe`, `dist/`)
- Add `README.md` stub (project name and one-line description only)

#### T0.2 — Create directory skeleton
- Create all directories as defined in architecture:
  ```
  cmd/errorprobe/          ← package main; binary entry point
  cmd/                     ← Cobra command definitions (package cmd)
  internal/config/
  internal/docker/
  internal/stack/
  internal/discovery/
  internal/configgen/
  internal/ingest/
  internal/health/
  internal/tui/
  internal/logger/
  assets/templates/
  ```
- `cmd/errorprobe/main.go`: `package main`; calls `cmd.Execute()`; this is the sole binary entry point — `go build ./cmd/errorprobe` produces the `errorprobe` executable
- Place a `doc.go` file with a package declaration in each `internal/` package (allows `go build ./...` to succeed immediately)
- `assets/templates/` is accessed via Go's `embed` package; add `assets/embed.go` with `//go:embed templates/*` and export an `FS embed.FS` variable — template files are then read from the embedded FS at runtime, not from loose disk files
- No implementation yet — stubs only

---

### Tier 2 — Core dependencies (depends on T0.1 only)

#### T0.3 — Add third-party dependencies
- Add Cobra (`github.com/spf13/cobra`) to `go.mod`
- Add Viper (`github.com/spf13/viper`) to `go.mod` (config loading)
- Add Lumberjack (`gopkg.in/natefinch/lumberjack.v2`) to `go.mod` (rotating logs)
- Add testify (`github.com/stretchr/testify`) to `go.mod` (test assertions)
- Run `go mod tidy`; commit `go.sum`

---

### Tier 3 — Package implementations (depends on T0.1, T0.2, T0.3; independent of each other)

#### T0.4 — Implement `internal/logger` package
- Public API:
  - `Init(path string, maxSizeMB int, maxBackups int) error`
  - `Info(msg string, fields ...any)`
  - `Error(msg string, fields ...any)`
  - `Debug(msg string, fields ...any)`
- Writes to `path` (rotating via Lumberjack) and to stdout (Info/Error only)
- Default path: `~/.errorprobe/logs/errorprobe.log`
- Log format: `2006-01-02T15:04:05Z07:00 LEVEL message [key=value ...]`
- No external logging framework — standard `log` package wrapped; keep the dependency surface minimal

#### T0.5 — Implement `internal/config` package
- Define typed Go struct `Config` covering the full `errorprobe.yaml` schema (architecture §8.2)
- Load precedence: `./errorprobe.yaml` → `~/.errorprobe/config.yaml` → built-in defaults
- Built-in defaults embedded in code (not a file):
  - Vector image: `timberio/vector:0.38.0-alpine`
  - Loki image: `grafana/loki:3.0.0`, port 3100, retention 72h
  - Grafana image: `grafana/grafana:11.0.0`, port 3000
  - Ingest: transport `http`, port 9099, bind `127.0.0.1`
  - Severity patterns: `error: [ERROR, FATAL, panic, Exception, error]`, `warn: [WARN, WARNING, warn]`
  - Check: fail_on `HAS_ERRORS`
- `version` field validation: reject any value other than `1` with a clear error message
- Public API:
  - `Load(projectDir string) (*Config, error)`
  - `Config.StateDir() string` → `~/.errorprobe/state/`
  - `Config.ConfigsDir() string` → `~/.errorprobe/configs/`
  - `Config.LogsDir() string` → `~/.errorprobe/logs/`

#### T0.6 — Implement state directory initialisation
- Standalone function `EnsureDirs(cfg *Config) error` in `internal/config` package
- Creates `~/.errorprobe/{configs,state,logs}/` if they do not exist
- Idempotent: no error if directories already exist
- Called once at startup before any command runs

---

### Tier 4 — CLI wiring (depends on T0.4, T0.5, T0.6)

#### T0.7 — Create `cmd/errorprobe/main.go` entry point
- `package main` in `cmd/errorprobe/main.go`
- Imports `cmd` package and calls `cmd.Execute()`; exits with non-zero code on error
- This is the only file in `package main`; all command logic lives in `package cmd`
- Build command: `go build -o errorprobe.exe ./cmd/errorprobe` (Windows); `go build -o errorprobe ./cmd/errorprobe` (Linux/macOS)

#### T0.8 — Wire Cobra root command
- `cmd/root.go`: root command with `--config` flag (override config file path), `--debug` flag (verbose logging)
- `PersistentPreRunE` on root: call `config.Load()`, call `config.EnsureDirs()`, call `logger.Init()`
- Version flag: `--version` prints `errorprobe dev` (real version injected at build time via `-ldflags` in Phase Distribution)

#### T0.9 — Wire Cobra subcommand stubs
- One file per command under `cmd/`:
  - `cmd/up.go` — prints `"up: not implemented"`
  - `cmd/down.go` — prints `"down: not implemented"`
  - `cmd/reload.go` — prints `"reload: not implemented"`
  - `cmd/update.go` — prints `"update: not implemented"`
  - `cmd/list.go` — prints `"list: not implemented"`
  - `cmd/status.go` — prints `"status: not implemented"`
  - `cmd/watch.go` — prints `"watch: not implemented"`
  - `cmd/logs.go` — prints `"logs: not implemented"`
  - `cmd/check.go` — prints `"check: not implemented"`
- Each stub: correct `Use`, `Short`, and `Long` descriptions already written (not filler text)
- `logs` command: accepts `<container>` positional arg and `--errors-only` flag in stub already

---

### Tier 5 — Unit tests (depends on T0.4, T0.5, T0.6; independent of each other)

#### T0.10 — Unit tests: `internal/logger`
- `TestInit_CreatesLogFile`: call `Init` with a temp path; assert file is created
- `TestInit_Idempotent`: call `Init` twice; assert no error
- `TestInfo_WritesToFile`: call `Info`; assert message appears in log file
- `TestError_WritesToFile`: call `Error`; assert message appears in log file with ERROR level
- `TestDebug_SuppressedByDefault`: call `Debug` without debug flag; assert message does not appear in log file

#### T0.11 — Unit tests: `internal/config`
- `TestLoad_Defaults`: no yaml file present; assert all default values are populated correctly
- `TestLoad_ProjectLocal_Overrides_Global`: both files present; assert project-local values win
- `TestLoad_Global_Overrides_Defaults`: only global file present; assert global values override defaults
- `TestLoad_InvalidVersion`: yaml with `version: 2`; assert error returned with message containing "unsupported version"
- `TestLoad_UnknownField`: yaml with unrecognised field; assert error or warning (decide: strict or lenient — document the choice)
- `TestLoad_PartialOverride`: yaml sets only `stack.loki.port`; assert all other fields remain at defaults
- `TestStateDirPaths`: assert `StateDir()`, `ConfigsDir()`, `LogsDir()` return correct OS paths

#### T0.12 — Unit tests: `internal/config.EnsureDirs`
- `TestEnsureDirs_CreatesMissingDirs`: point at a temp base dir; assert all three subdirs are created
- `TestEnsureDirs_Idempotent`: call twice; assert no error on second call
- `TestEnsureDirs_ExistingDirs`: pre-create dirs; assert no error

---

### Tier 6 — Build validation (depends on all above)

#### T0.13 — Build and lint pass
- `go build ./...` produces no errors
- `go vet ./...` produces no warnings
- `golangci-lint run` passes (add `.golangci.yml` with standard ruleset: `errcheck`, `govet`, `staticcheck`, `gofmt`)
- `go test ./... -cover` reports ≥ 90% coverage on `internal/config` and `internal/logger`
- `errorprobe --help` output lists all 9 subcommands with correct descriptions

---

### Final Task

#### T0.14 — Mark phase complete in work-plan.md
- Open `docs/work-plan.md`
- Mark all Phase 0 tasks as `[x]`
- Add completion date next to phase heading

#### T0.15 — Update roadmap.html
- Open `docs/roadmap.html` in a browser and verify Phase 0 is reflected correctly
- In the `PHASES` array, set Phase 0's `status` to `"completed"` and `actualEnd` to the actual finish date
- Compare actual duration against the planned estimate; if velocity differed, adjust `start` / `end` for all subsequent phases accordingly
- Update the `TODAY` constant to the current date
- Recompute and document the revised total story-point burn rate and projected completion date for the remaining phases in a comment above the `PHASES` array

---

## Deliverables

| Deliverable | Description |
|---|---|
| Go module | `go.mod` / `go.sum` with all Phase 0 dependencies |
| `cmd/errorprobe/main.go` | Binary entry point; `package main`; calls `cmd.Execute()` |
| Package skeleton | All `internal/` packages with `doc.go` stubs; compiles cleanly |
| `assets/embed.go` | `//go:embed templates/*` exporting `FS embed.FS`; templates accessible at runtime from the binary |
| `internal/config` | Full config loading, validation, defaults, path helpers |
| `internal/logger` | Rotating file logger, stdout echo for Info/Error |
| `config.EnsureDirs` | State directory initialisation |
| Cobra CLI | Root command + 9 subcommand stubs with correct metadata |
| Unit tests | ≥ 90% coverage on `config` and `logger` packages |
| `.golangci.yml` | Linter config committed to repo |
| `go build ./cmd/errorprobe` | Produces runnable `errorprobe` binary without errors or warnings |

---

## Manual Tests

Run after all tasks are complete:

1. **`errorprobe --help`** — output lists all 9 subcommands; each has a non-filler description.
2. **`errorprobe up`** — prints `"up: not implemented"` and exits 0.
3. **`errorprobe --version`** — prints `"errorprobe dev"`.
4. **No `errorprobe.yaml` present** — run any command; tool starts without error; no crash on missing config.
5. **`errorprobe.yaml` with `version: 2`** — tool prints a clear error message referencing unsupported version and exits non-zero.
6. **State directories** — after running any command, confirm `~/.errorprobe/{configs,state,logs}/` all exist.
7. **Log file** — after running any command, confirm `~/.errorprobe/logs/errorprobe.log` exists and contains at least one log line.
8. **`go test ./... -cover`** — all tests pass; coverage ≥ 90% on `internal/config` and `internal/logger`.
