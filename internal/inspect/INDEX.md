# Service views

> **Purpose**: Service views
> **Next**: `service.go`
> **Boundary**: Public registry projections; CLI rendering is in ../../cmd/.

- `service.go`: configured preserve_host projection for recipes and local service views; `EndpointView.ExactURL`/`ExactHTTPSURL` are the one definition of an endpoint with a usable exact URL (https and public_https share the node URL).
- `endpoint_url_test.go`: exact-URL predicate cases (tcp, unknown kind, expected state, template host).
- `access_log_codes.go`: warning codes for dropped records and unavailable local history.

# Browser guest links

- guest_codes.go: expiry and unavailable Funnel warning codes.
