# CI policy scripts

- [ci-tier.py](ci-tier.py): trusted-base PR classification from Git data, with release/embed/symlink exclusions and CI_PR_TIER_MODE. Chinese duplicate document paths require the full documentation contract gate.
- [test_ci_tier.py](test_ci_tier.py): hermetic classification, aggregation, workflow wiring and target-loop tests.

Run locally with `python3 -m unittest discover -s .github/scripts -p 'test_ci_tier.py' -v`.
