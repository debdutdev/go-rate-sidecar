# Contributing

Thanks for your interest in contributing to rate-limiter!

## Getting Started

```bash
git clone https://github.com/debdutdev/rate-limiter.git
cd rate-limiter
make test
```

Requires Go 1.24 or later.

## Development

```bash
make test    # run all tests with race detector
make bench   # run benchmarks
make vet     # run go vet
make build   # compile all packages
```

## Submitting Changes

1. Fork the repository and create a feature branch from `main`.
2. Add tests for any new functionality.
3. Run `make test` and `make vet` — all checks must pass.
4. Keep commits focused. One logical change per commit.
5. Open a pull request with a clear description of what changed and why.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Add doc comments on all exported types, functions, and methods.
- Prefer returning errors over panicking in library code.
- Keep the public API surface small — not every helper needs to be exported.

## Reporting Issues

Open an issue on GitHub with:
- What you expected to happen
- What actually happened
- Steps to reproduce
- Go version and OS
