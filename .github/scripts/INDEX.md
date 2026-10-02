# CI policy scripts

- [ci-tier.py](ci-tier.py): conservative PR classification from immutable base/head Git trees.
- [test_ci_tier.py](test_ci_tier.py): hermetic classification, aggregation, workflow wiring and target-loop tests.

Run locally with `python3 -m unittest discover -s .github/scripts -p 'test_ci_tier.py' -v`.
