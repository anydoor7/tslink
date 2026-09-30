package duration

import "time"

// Value is a duration flag that reads Parse and retains pflag's duration type.
type Value time.Duration

// NewValue returns a flag with the given default.
func NewValue(value time.Duration) *Value {
	flag := Value(value)
	return &flag
}

func (d *Value) Set(value string) error {
	parsed, err := Parse(value)
	if err != nil {
		return err
	}
	*d = Value(parsed)
	return nil
}

func (d *Value) Type() string   { return "duration" }
func (d *Value) String() string { return time.Duration(*d).String() }
