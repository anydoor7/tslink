# CI policy scripts

- [ci-tier.py](ci-tier.py): trusted-base PR classification from Git data, with release/embed/symlink exclusions and CI_PR_TIER_MODE. Chinese duplicate document paths require the full documentation contract gate.
- [test_ci_tier.py](test_ci_tier.py): hermetic classification, aggregation, workflow wiring and target-loop tests.
- `convergence-diagnostic.py`: exact-tree diagnostic command runner and mandatory collector; records failures and continues independent commands.
- `diagnostic-skips.json`: exact pre-existing skip source sites (platform capabilities, explicit opt-ins and helper processes). Unknown or unattributed skips fail reconciliation; required business tests must pass regardless.
- `test_convergence_diagnostic.py`: complete positive fixture, independent failure mutations, fresh-process and collect-all execution controls.

Run locally with `python3 -m unittest discover -s .github/scripts -p 'test_ci_tier.py' -v`.
