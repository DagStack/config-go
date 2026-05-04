# Contributing

Thanks for your interest! A few light conventions to keep review fast.

## Environment

- Go 1.22+ (see `go.mod`).
- `make test` before commit — required.
- `golangci-lint` — optional, but appreciated.

## Code style

- `gofmt -s` is required (`make fmt`).
- Public types and functions get doc comments in English, starting with the subject's name (`// Config is the primary handle...`).
- Tests live in the `config_test` package (external test package) so they exercise only the public API. White-box access is reserved for test helpers (`testhelpers_test.go`).

## Commit structure

- Conventional Commits: `feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`.
- First line — imperative, English. The body may use Russian (rationale, motivation, links to issues).
- No `Co-Authored-By Claude`, no AI attribution.

## Identity for dagstack repositories

```bash
git config user.name "Evgenii Demchenko"
git config user.email "demchenkoev@gmail.com"
```

## Spec submodule

`spec/` is a submodule pointing at `dagstack/config-spec`. A SHA bump is its own commit:

```bash
cd spec && git pull origin main && cd ..
git add spec
git commit -m "chore(spec): bump config-spec to <sha> — <short description>"
```

If the spec's directory layout changes (`conformance`, `_meta/`), the implementation must run `make conformance` locally.

## Roadmap-driven phases

Implementation lands in phases (see README). A new phase is its own PR with a checklist:

- [ ] All previous phases are complete and merged.
- [ ] `make test` green.
- [ ] `make vet` clean.
- [ ] `make conformance` green (starting at Phase D).
- [ ] `CHANGELOG.md` updated.
- [ ] Architect review passed.
