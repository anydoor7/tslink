# launchctl fixtures

> Purpose: real macOS launchctl output for daemon supervision regressions.
> Read when: changing launchd state or disabled-override parsing.
> Next: ../../supervision_r2_darwin_test.go.
> Stop when: runtime installation or reboot validation is required.
> Boundary: captured text only; tests never run a production launchctl mutation.

- `launchctl-print-prod.txt`: synthetic launchctl print fixture with nested resource/jetsam coalition `state = active` records.
- `launchctl-print-disabled-prod.txt`: synthetic disabled-services fixture retaining `=> enabled/disabled` values for parser tests.

Source: synthetic launchctl parser fixtures. SHA-256:

- launchctl-print-prod.txt: `4a14ab56188ce327815bed9ecd39c609990d014c6aa94e2c6112b17fbb06cd05`
- launchctl-print-disabled-prod.txt: `fbfdd488edb4e0b5b067e905ec44b4c7dbfd19ccec54928977f73adb9d8792b5`
