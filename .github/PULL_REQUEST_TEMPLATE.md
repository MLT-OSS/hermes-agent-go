## What does this PR do?

<!-- Describe the change clearly. What problem does it solve? Why is this approach the right one? -->



## Related Issue

<!-- Link the issue this PR addresses. Use "Fixes #N" to auto-close the issue on merge. -->

Fixes #

## Type of Change

<!-- Check all that apply. -->

- [ ] 🐛 Bug fix (non-breaking change that fixes an issue)
- [ ] ✨ New feature (non-breaking change that adds functionality)
- [ ] 🔒 Security fix
- [ ] ⚡ Performance improvement
- [ ] ♻️ Refactor (no behavior change)
- [ ] 📝 Documentation update
- [ ] ✅ Tests (adding or improving test coverage)
- [ ] 🔧 Build / CI / tooling
- [ ] 🎯 New skill (bundled)

## Changes Made

<!-- List the specific changes. Include file paths and a brief description for each. -->

| File | Change |
|------|--------|
| `internal/...` | ... |

## How to Test

<!-- Steps to verify this change works correctly. For bugs: include reproduction steps + proof the fix works. -->

1. Build: `go build -o hermes ./cmd/hermes/`
2. ...
3. Expected result: ...

## Build & Test Verification

<!-- Complete all applicable checks before requesting review. -->

### Required

- [ ] `go build ./cmd/hermes/` — compiles without errors
- [ ] `go test ./...` — all tests pass
- [ ] `go vet ./...` — no issues
- [ ] No unrelated changes included in this PR

### Cross-platform (if touching build tags, CGo, or platform-specific code)

- [ ] `GOOS=linux GOARCH=amd64 go build ./cmd/hermes/` — Linux OK
- [ ] `GOOS=darwin GOARCH=arm64 go build ./cmd/hermes/` — macOS OK
- [ ] `GOOS=windows GOARCH=amd64 go build ./cmd/hermes/` — Windows OK

### Quality (strongly encouraged)

- [ ] `go test -race ./...` — no race conditions
- [ ] New code has test coverage (unit tests for new functions/methods)
- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`fix(scope):`, `feat(scope):`, etc.)

### Documentation

- [ ] Updated README.md if public-facing behavior changed — or N/A
- [ ] Updated tool descriptions/schemas if tool behavior changed — or N/A
- [ ] Updated config examples if config keys were added/changed — or N/A

## For New Skills

<!-- Fill this section ONLY if adding a bundled skill. Delete otherwise. -->

- [ ] SKILL.md follows the standard format (YAML frontmatter + Markdown body)
- [ ] Skill is broadly useful to most users
- [ ] No external dependencies beyond what's already available
- [ ] Tested end-to-end: `./hermes chat "Use the X skill to do Y"`

## Screenshots / Logs

<!-- If applicable, add screenshots, terminal output, or test results showing the change in action. -->

