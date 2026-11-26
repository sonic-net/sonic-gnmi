"""Run the existing Python client tests in the declared environment."""
import sys
import pytest

if __name__ == "__main__":
    raise SystemExit(pytest.main(sys.argv[1:]))
