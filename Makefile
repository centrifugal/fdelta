# Development flow for fdelta. CI runs these same targets, so what passes here
# passes there.
#
# Run `make` or `make help` for the list.

GO ?= go

# Tool versions are pinned so that a lint result is a property of the code and
# not of the day it was run. Bump them deliberately, and read what the new
# version reports before doing so: these tools do add checks between releases.
GOLANGCI_LINT_VERSION ?= v2.13.2
GOSEC_VERSION         ?= v2.22.9
GOVULNCHECK_VERSION   ?= latest

GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOSEC         := $(GO) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
GOVULNCHECK   := $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# How long each fuzz target runs. CI uses a short time per push and a long one
# on its weekly schedule.
FUZZTIME ?= 30s

# Statement coverage below this fails `make cover`. The uncovered remainder is
# a branch reachable only where int is 32 bits, which `make cross` exercises.
COVERAGE_MIN ?= 98

FUZZ_TARGETS      := FuzzRoundTrip FuzzApply FuzzAppendForms FuzzAppendApplyPreservesDst
FUZZ_CREF_TARGETS := FuzzCRefRoundTrip FuzzCRefApplyAgrees FuzzCRefArbitraryValidDeltas

# Platforms worth testing beyond the developer's own: two where int is 32 bits,
# one big-endian. Needs Docker, and QEMU for the emulated ones.
CROSS_TARGETS := 386:linux/386:i386/debian:bookworm-slim \
                 arm:linux/arm/v7:arm32v7/debian:bookworm-slim \
                 s390x:linux/s390x:s390x/debian:bookworm-slim

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "fdelta development targets:"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Variables: FUZZTIME=$(FUZZTIME) COVERAGE_MIN=$(COVERAGE_MIN)"

# --- building and testing --------------------------------------------------

.PHONY: build
build: ## Build the package
	$(GO) build ./...

.PHONY: test
test: ## Run the test suite
	$(GO) test -count=1 ./...

.PHONY: test-short
test-short: ## Run the suite without the exhaustive differential tests
	$(GO) test -count=1 -short ./...

.PHONY: test-race
test-race: ## Run the suite under the race detector
	$(GO) test -count=1 -race ./...

.PHONY: test-cref
test-cref: ## Additionally check against Fossil's src/delta.c (needs a C toolchain)
	$(GO) test -count=1 -tags cref ./...
	$(GO) test -count=1 -race -tags cref ./...

.PHONY: test-purego
test-purego: ## Build and test with cgo disabled
	CGO_ENABLED=0 $(GO) build ./...
	CGO_ENABLED=0 $(GO) vet ./...
	CGO_ENABLED=0 $(GO) test -count=1 ./...

.PHONY: vet
vet: ## Run go vet, with and without the cref tag
	$(GO) vet ./...
	$(GO) vet -tags cref ./...
	$(GO) vet -tags exhaustive ./...
	$(GO) vet -tags jsref ./...

# --- correctness guarantees ------------------------------------------------

.PHONY: no-cgo
no-cgo: ## Assert the package uses no cgo and no unsafe, and has no dependencies
	@files=$$($(GO) list -f '{{.CgoFiles}}' ./...); \
	if [ "$$files" != "[]" ]; then echo "cgo files in the importable packages: $$files"; exit 1; fi
	@if $(GO) list -f '{{join .Imports "\n"}}{{"\n"}}{{join .TestImports "\n"}}' ./... | grep -qx unsafe; then \
		echo "the package now imports unsafe"; exit 1; fi
	@pkgs=$$($(GO) list ./...); \
	if [ "$$pkgs" != "github.com/centrifugal/fdelta" ]; then \
		echo "expected exactly one importable package, got:"; echo "$$pkgs"; exit 1; fi
	@if grep -qE '^[[:space:]]*require' go.mod; then \
		echo "go.mod has grown a dependency:"; cat go.mod; exit 1; fi
	@echo "no cgo, no unsafe, no dependencies, one importable package"

.PHONY: portable
portable: ## Cross-compile for every platform Go supports that matters here
	@set -e; for t in windows/amd64 windows/arm64 darwin/arm64 linux/386 \
	                  linux/s390x linux/mips linux/ppc64 js/wasm wasip1/wasm \
	                  plan9/amd64 freebsd/riscv64; do \
		echo "  building for $$t"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} $(GO) build ./...; \
	done

.PHONY: cross
cross: ## Run the suite on 32-bit and big-endian platforms under Docker
	@set -e; for spec in $(CROSS_TARGETS); do \
		arch=$$(echo $$spec | cut -d: -f1); \
		plat=$$(echo $$spec | cut -d: -f2); \
		img=$$(echo $$spec | cut -d: -f3-); \
		echo "==> $$plat"; \
		GOOS=linux GOARCH=$$arch GOARM=7 $(GO) test -c -o fdelta-$$arch.test .; \
		docker run --rm --platform $$plat -v "$$PWD:/w" -w /w $$img \
			./fdelta-$$arch.test -test.count=1 -test.short -test.timeout 600s; \
		rm -f fdelta-$$arch.test; \
	done

.PHONY: interop-js
interop-js: ## Check our deltas against the JavaScript decoder browsers run (needs node)
	$(GO) test -count=1 -tags jsref -run TestJSRef -v .

.PHONY: exhaustive
exhaustive: ## Verify the codec and checksum over their whole domains (~10s)
	$(GO) test -tags exhaustive -count=1 -run TestExhaustive -timeout 900s -v .

.PHONY: cover
cover: ## Measure coverage and fail below COVERAGE_MIN
	$(GO) test -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1
	@pct=$$($(GO) tool cover -func=coverage.out | tail -1 | grep -oE '[0-9.]+%' | tr -d '%'); \
	awk -v p="$$pct" -v m="$(COVERAGE_MIN)" 'BEGIN { if (p < m) { print "coverage " p "% is below " m "%"; exit 1 } }'

# --- fuzzing ---------------------------------------------------------------

.PHONY: fuzz
fuzz: ## Fuzz every target for FUZZTIME each
	@set -e; for t in $(FUZZ_TARGETS); do \
		echo "==> $$t ($(FUZZTIME))"; \
		$(GO) test -run '^$$' -fuzz "^$$t$$" -fuzztime $(FUZZTIME) .; \
	done

.PHONY: fuzz-cref
fuzz-cref: ## Fuzz differentially against Fossil's src/delta.c
	@set -e; for t in $(FUZZ_CREF_TARGETS); do \
		echo "==> $$t ($(FUZZTIME))"; \
		$(GO) test -tags cref -run '^$$' -fuzz "^$$t$$" -fuzztime $(FUZZTIME) .; \
	done

# --- linting and security --------------------------------------------------

.PHONY: fmt
fmt: ## Format the source
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if anything needs formatting
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "these files need gofmt:"; echo "$$out"; exit 1; fi
	@echo "gofmt clean"

.PHONY: lint
lint: fmt-check ## Run golangci-lint, with and without the cref tag
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) run --build-tags=cref ./...

.PHONY: sec
sec: ## Run gosec and govulncheck
	$(GOSEC) -exclude-dir=internal/cref ./...
	$(GOVULNCHECK) ./...

.PHONY: sec-sarif
sec-sarif: ## Run gosec writing SARIF, for upload to code scanning
	$(GOSEC) -exclude-dir=internal/cref -fmt=sarif -out=gosec.sarif -stdout -verbose=text ./...

# --- benchmarks ------------------------------------------------------------

.PHONY: bench
bench: ## Benchmark Create and Apply
	$(GO) test -run XXX -bench 'BenchmarkCreate$$|BenchmarkApply$$' -benchtime 1s -count 3 .

.PHONY: bench-adversarial
bench-adversarial: ## Benchmark the high-entropy case the block index guards
	$(GO) test -run XXX -bench BenchmarkCreateAdversarial -benchtime 3x .

# --- maintenance -----------------------------------------------------------

.PHONY: golden
golden: ## Regenerate testdata/golden.txt, then show the diff to review
	$(GO) test -run TestGolden -update .
	@git --no-pager diff -- testdata/golden.txt || true
	@echo
	@echo "Read that diff before committing: it is a change in the bytes every client receives."

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build and coverage artefacts
	rm -f coverage.out gosec.sarif fdelta-*.test
	$(GO) clean -testcache

# --- the gate --------------------------------------------------------------

.PHONY: check
check: fmt-check vet no-cgo test test-short test-race test-purego cover lint sec ## Everything CI runs, except cref/cross/fuzz
	@echo
	@echo "check passed. For a change to the encoder or decoder also run:"
	@echo "  make test-cref    (needs a C toolchain)"
	@echo "  make fuzz         (FUZZTIME=2m for something thorough)"
	@echo "  make cross        (needs Docker)"
	@echo "  make exhaustive   (the whole 32-bit domain, about ten seconds)"
	@echo "  make interop-js   (the decoder browsers run; needs node)"

.PHONY: check-all
check-all: check test-cref interop-js exhaustive portable fuzz fuzz-cref cross ## Everything, including cref, exhaustive, cross-platform and fuzzing
	@echo
	@echo "check-all passed."
