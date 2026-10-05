# Test wait checks

- `testwait.go`: hang guards for asynchronous effects, bounded by the test binary deadline rather than a short real-time limit.
- `testwait_test.go`: budget, receive and poll behavior, including named expiry.
