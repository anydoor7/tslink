# Global configuration

- `config.go`: isolated configuration paths and locked strict global config persistence.
- `durations.go`: validates durations.public_max and returns the shared lifetime policy.
- `durations_test.go`: strict input, load/save refusal and preservation evidence.
- Other platform paths and existing regression tests retain their current files.
