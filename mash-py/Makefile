.PHONY: help install test test-unit test-interop test-conformance lint typecheck format generate clean

PYTHON ?= python3
VENV   ?= .venv
PIP    := $(VENV)/bin/pip
PY     := $(VENV)/bin/python
PYTEST := $(VENV)/bin/pytest
RUFF   := $(VENV)/bin/ruff
MYPY   := $(VENV)/bin/mypy

help:
	@echo "Targets:"
	@echo "  install         - create venv and install dev dependencies"
	@echo "  test            - run unit tests (skips interop and conformance)"
	@echo "  test-unit       - same as 'test'"
	@echo "  test-interop    - run tests that require a running mash-go binary"
	@echo "  test-conformance- run mash-go's mash-test against a Python device"
	@echo "  lint            - ruff check"
	@echo "  typecheck       - mypy"
	@echo "  format          - ruff format + ruff check --fix"
	@echo "  generate        - regenerate code from docs/features YAML"
	@echo "  clean           - remove venv and caches"

$(VENV)/bin/activate:
	$(PYTHON) -m venv $(VENV)
	$(PIP) install --upgrade pip
	$(PIP) install -e '.[dev]'

install: $(VENV)/bin/activate

test test-unit: install
	$(PYTEST) -m "not interop and not conformance" tests/

test-interop: install
	$(PYTEST) -m interop tests/

test-conformance: install
	$(PYTEST) -m conformance tests/

lint: install
	$(RUFF) check mash_py/ tests/ codegen/

typecheck: install
	$(MYPY) mash_py/ codegen/

format: install
	$(RUFF) format mash_py/ tests/ codegen/
	$(RUFF) check --fix mash_py/ tests/ codegen/

generate: install
	$(PY) -m codegen --features-dir ../docs/features --usecases-dir ../docs/usecases/1.0 --output mash_py/

clean:
	rm -rf $(VENV) .pytest_cache .ruff_cache .mypy_cache
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
	find . -type d -name '*.egg-info' -exec rm -rf {} + 2>/dev/null || true
