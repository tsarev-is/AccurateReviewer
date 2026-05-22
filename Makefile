# AccurateReviewer — AI code review tool driven by BDD feature files.
# The feature files in bdd/ are the single source of truth for the tool's
# behaviour. The Go code in cmd/ and internal/ is one possible implementation.

GO := $(shell which go 2>/dev/null \
        || find /usr/local /opt/homebrew/bin -name go -type f 2>/dev/null \
           | head -1 \
        || echo go)

BIN_DIR    := bin
CLI_BIN    := $(BIN_DIR)/accurate-reviewer
MOCK_BIN   := $(BIN_DIR)/mock-llm

.PHONY: help setup setup-python build clean \
        test-cli test-secrets test-sanitizer test-diff test-analyzer \
        test-review test-config test-all

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "  setup         Install Python deps + resolve Go modules"
	@echo "  build         Compile Go binaries into $(BIN_DIR)/"
	@echo ""
	@echo "  test-cli         CLI command surface (init/analyze/review)"
	@echo "  test-secrets     Deterministic secrets scanner"
	@echo "  test-sanitizer   Prompt-injection sanitizer"
	@echo "  test-diff        Diff parsing & filtering"
	@echo "  test-analyzer    Project startup analysis"
	@echo "  test-review      Master + worker review (mock LLM)"
	@echo "  test-config      .review.yml parsing & validation"
	@echo "  test-all         Run the full BDD suite"
	@echo ""
	@echo "  clean         Remove compiled binaries"

setup-python:
	@echo "Setting up Python virtual environment..."
	python3 -m venv .venv
	@bash -c "source .venv/bin/activate && \
		pip install -q -r requirements.txt"

setup: setup-python
	@echo "Go binary: $(GO)"
	@$(GO) version
	@echo "Resolving Go module dependencies..."
	$(GO) mod tidy
	@mkdir -p $(BIN_DIR)

build: setup
	@echo "Building Go binaries..."
	$(GO) build -o $(CLI_BIN)  ./cmd/accurate-reviewer/
	$(GO) build -o $(MOCK_BIN) ./cmd/mock-llm/
	@echo "Build complete."

# Per-feature targets. Each maps to a Gherkin tag.

test-cli:       build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@cli"

test-secrets:   build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@secrets"

test-sanitizer: build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@sanitizer"

test-diff:      build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@diff"

test-analyzer:  build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@analyzer"

test-review:    build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@review"

test-config:    build
	@bash -c "source .venv/bin/activate && behave bdd/ --tags=@config"

test-all:       build
	@bash -c "source .venv/bin/activate && behave bdd/"

clean:
	@rm -rf $(BIN_DIR)/
	@echo "Cleaned."
