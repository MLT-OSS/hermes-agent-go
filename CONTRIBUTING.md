# Contributing to Hermes Agent Go

Thank you for your interest in contributing! This document covers the guidelines and conventions for the project.

## Getting Started

### Prerequisites

- Go 1.22 or later
- Git

### Building

```bash
git clone https://github.com/MLT-OSS/hermes-agent-go.git
cd hermes-agent-go
go build -o hermes ./cmd/hermes/
```

### Running Tests

```bash
go test ./...              # All tests
go test ./... -v           # Verbose
go test ./... -cover       # With coverage
go test -race ./...        # Race condition detection
go vet ./...               # Static analysis
```

## Development Workflow

1. **Fork** the repository
2. **Create a branch** from `main`: `git checkout -b feat/my-feature`
3. **Make your changes** with clear, focused commits
4. **Run tests**: `go test ./... && go vet ./...`
5. **Cross-compile check** (if touching platform-specific code):
   ```bash
   GOOS=linux go build ./cmd/hermes/
   GOOS=darwin go build ./cmd/hermes/
   GOOS=windows go build ./cmd/hermes/
   ```
6. **Submit a PR** using the [PR template](.github/PULL_REQUEST_TEMPLATE.md)

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(agent): add context compression threshold config
fix(gateway): wire signal.Notify for graceful shutdown
test(tools): add approval pattern matching tests
docs: update README with cross-compilation instructions
refactor(llm): extract provider detection into separate function
```

**Scopes**: `agent`, `tools`, `gateway`, `cli`, `llm`, `config`, `state`, `skills`, `acp`, `cron`, `batch`

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `slog` for structured logging (not `fmt.Println` or `log`)
- Prefer `internal/` packages — all code is internal to the binary
- Use functional options pattern for configurable types (see `agent.AgentOption`)
- Tool registration uses `init()` + global registry (see `internal/tools/registry.go`)
- Error handling: wrap errors with context using `fmt.Errorf("operation: %w", err)`

## Project Structure

```
cmd/hermes/              CLI entry point (Cobra)
internal/
├── agent/               Core agent loop, streaming, prompts
├── acp/                 ACP editor integration server
├── batch/               Batch trajectory generation
├── cli/                 Interactive TUI, commands, setup
├── config/              Configuration loading, profiles
├── cron/                Scheduled task management
├── gateway/             Multi-platform messaging gateway
│   └── platforms/       Platform adapters (Telegram, Discord, etc.)
├── llm/                 LLM client (OpenAI + Anthropic)
├── plugins/             Plugin discovery
├── skills/              Skill loading, parsing, hub
├── state/               SQLite session persistence
├── tools/               Tool implementations
│   └── environments/    Terminal backends (local, Docker, SSH, etc.)
├── toolsets/            Tool grouping and resolution
└── utils/               Shared utilities
skills/                  Bundled skill files
optional-skills/         Official optional skills
```

## Testing Guidelines

- **Unit tests** go in `*_test.go` alongside the code they test
- **Table-driven tests** are preferred for functions with multiple input/output cases
- **Test helpers** should use `t.Helper()` for clean error reporting
- Mock external dependencies (API calls, file system) rather than hitting real services
- Aim for meaningful coverage of critical paths, not 100% line coverage

## Should It Be a Skill or a Tool?

- **Tools** are Go functions registered in `internal/tools/` that the LLM can invoke. They handle generic capabilities (terminal execution, file operations, web search).
- **Skills** are Markdown/YAML files in `skills/` that provide domain-specific instructions and workflows. They are loaded into the system prompt when relevant.

If your contribution is a specialized integration (specific API, niche workflow), it should be a **skill**, not a tool.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
