# Workflow inventory

- `release-candidate.yml`: release acceptance gate; the native job runs each test property only on the runners where it can differ.
- `convergence-diagnostic.yml`: manual immutable-SHA collect-all pilot, six independent families, four runners maximum, strict final reconciliation.
